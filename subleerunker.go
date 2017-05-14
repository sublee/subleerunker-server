// A simple HTTP server for world-best-score in SUBLEERUNKER.  It remembers
// a score for about a week.
package subleerunker

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)
import (
	"golang.org/x/net/context"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
)

const TTL time.Duration = 7 * 24 * time.Hour // 7 days

type Champion struct {
	Score     int
	Name      string
	Token     string
	ExpiresAt time.Time
}

var NoChampion = &Champion{0, "", "", time.Time{}}

type NotHigherScore struct {
	Score     int
	PrevScore int
}

func (n *NotHigherScore) Error() string {
	return fmt.Sprintf(
		"score %d is not higher than prev score %d",
		n.Score, n.PrevScore,
	)
}

type NotAuthorized struct {
}

func (n *NotAuthorized) Error() string {
	return "not authorized"
}

func init() {
	fs := http.FileServer(http.Dir("."))
	http.Handle("/", fs)
	http.HandleFunc("/champion", HandleChampion)
}

func GetKey(c context.Context) *datastore.Key {
	return datastore.NewKey(c, "champion", "champion", 0, nil)
}

func IssueToken(seed int64) string {
	data := make([]byte, 8)
	binary.PutVarint(data, seed)
	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:])
}

func WriteResult(w http.ResponseWriter, result interface{}) {
	output, err := json.Marshal(result)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(output)
}

// Loads the current best score from the Google Cloud Datastore.
// Returns (score, name, authorized, err).
func LoadChampion(c context.Context, at time.Time) (*Champion, error) {
	key := GetKey(c)
	champion := new(Champion)
	err := datastore.Get(c, key, champion)
	if err == datastore.ErrNoSuchEntity || at.After(champion.ExpiresAt) {
		return NoChampion, nil
	} else if err != nil {
		return NoChampion, err
	}
	return champion, nil
}

// A handler for "GET /champion".
func GetChampion(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	champion, err := LoadChampion(c, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, token, _ := r.BasicAuth()
	WriteResult(w, struct {
		Score      int       `json:"score"`
		Name       string    `json:"name"`
		Authorized bool      `json:"authorized"`
		ExpiresAt  time.Time `json:"expiresAt"`
	}{
		champion.Score,
		champion.Name,
		champion.Token == token,
		champion.ExpiresAt,
	})
}

func WriteAuthorizedChampion(w http.ResponseWriter, champion *Champion) {
	WriteResult(w, struct {
		Score     int       `json:"score"`
		Name      string    `json:"name"`
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expiresAt"`
	}{
		champion.Score,
		champion.Name,
		champion.Token,
		champion.ExpiresAt,
	})
}

func NormalizeName(name string) string {
	name = strings.ToUpper(name)
	p := regexp.MustCompile("[A-Z]+")
	name = strings.Join(p.FindAllString(name, -1), "")
	if len(name) > 3 {
		name = name[:3]
	}
	return name
}

// A handler for "PUT /champion" to beat the previous record.
func BeatChampion(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	score, err := strconv.Atoi(r.FormValue("score"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	name = NormalizeName(name)
	at := time.Now()
	token := IssueToken(at.Unix())
	champion := &Champion{
		Score:     score,
		Name:      name,
		Token:     token,
		ExpiresAt: at.Add(TTL), // after 7 days
	}
	err = datastore.RunInTransaction(c, func(c context.Context) error {
		prevChampion, err := LoadChampion(c, at)
		if err != nil {
			return err
		}
		if score <= prevChampion.Score {
			return &NotHigherScore{
				Score:     score,
				PrevScore: prevChampion.Score,
			}
		}
		key := GetKey(c)
		_, err = datastore.Put(c, key, champion)
		return err
	}, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	WriteAuthorizedChampion(w, champion)
}

// A handler for "PUT /champion" to rename the current record.
func RenameChampion(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	name := r.FormValue("name")
	name = NormalizeName(name)
	_, token, _ := r.BasicAuth()
	at := time.Now()
	_champion := new(Champion)
	err := datastore.RunInTransaction(c, func(c context.Context) error {
		champion, err := LoadChampion(c, at)
		if err != nil {
			return err
		}
		if champion.Token != token {
			return &NotAuthorized{}
		}
		champion.Name = name
		key := GetKey(c)
		_, err = datastore.Put(c, key, champion)
		_champion = champion
		return err
	}, nil)
	switch err.(type) {
	case nil:
		break
	case *NotAuthorized:
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	WriteAuthorizedChampion(w, _champion)
}

// A combined handler for every methods of "/champion".
func HandleChampion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "https://sublee.github.io")
	switch strings.ToUpper(r.Method) {
	case "OPTIONS":
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")
	case "GET":
		GetChampion(w, r)
	case "PUT":
		if r.FormValue("score") != "" {
			BeatChampion(w, r)
		} else {
			RenameChampion(w, r)
		}
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
