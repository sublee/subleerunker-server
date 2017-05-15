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
	"google.golang.org/appengine/log"
)

const TTL time.Duration = 7 * 24 * time.Hour // 7 days

type Champion struct {
	Score      int
	Name       string
	Duration   time.Duration
	RecordedAt time.Time
	ExpiresIn  time.Duration
	Token      string
}

func (c *Champion) ExpiresAt() time.Time {
	return c.RecordedAt.Add(c.ExpiresIn)
}

func (c *Champion) IsExpired(t time.Time) bool {
	return t.After(c.ExpiresAt())
}

var NoChampion = &Champion{0, "", 0, time.Time{}, 0, ""}

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
func LoadChampion(c context.Context, t time.Time, ttl time.Duration) (*Champion, *datastore.Key, error) {
	parent := datastore.NewKey(c, "champions", "_", 0, nil)
	q := datastore.NewQuery("champion").Ancestor(parent).
		Filter("RecordedAt >", t.Add(-ttl)).
		Order("-RecordedAt").Limit(10)
	for i := q.Run(c); ; {
		var champion Champion
		key, err := i.Next(&champion)
		if err == datastore.Done {
			break
		}
		if err != nil {
			return NoChampion, nil, err
		} else if champion.IsExpired(t) {
			continue
		} else {
			return &champion, key, nil
		}
	}
	return NoChampion, nil, nil
}

// A handler for "GET /champion".
func GetChampion(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	champion, _, err := LoadChampion(c, time.Now(), TTL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, token, _ := r.BasicAuth()
	WriteResult(w, struct {
		Score      int       `json:"score"`
		Name       string    `json:"name"`
		ExpiresAt  time.Time `json:"expiresAt"`
		Authorized bool      `json:"authorized"`
	}{
		champion.Score,
		champion.Name,
		champion.ExpiresAt(),
		token != "" && token == champion.Token,
	})
}

func WriteAuthorizedChampion(w http.ResponseWriter, champion *Champion) {
	WriteResult(w, struct {
		Score     int       `json:"score"`
		Name      string    `json:"name"`
		ExpiresAt time.Time `json:"expiresAt"`
		Token     string    `json:"token"`
	}{
		champion.Score,
		champion.Name,
		champion.ExpiresAt(),
		champion.Token,
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
	duration, err := strconv.ParseFloat(r.FormValue("duration"), 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	name = NormalizeName(name)
	log.Debugf(
		c, "Trying to beat champion: %d by '%s' in %.3f sec",
		score, name, duration,
	)
	t := time.Now()
	token := IssueToken(t.Unix())
	champion := &Champion{
		Score:      score,
		Name:       name,
		Duration:   time.Duration(duration * float64(time.Second)),
		RecordedAt: t,
		ExpiresIn:  TTL,
		Token:      token,
	}
	var prevScore int
	var prevName string
	err = datastore.RunInTransaction(c, func(c context.Context) error {
		prevChampion, _, err := LoadChampion(c, t, TTL)
		if err != nil {
			return err
		}
		prevScore = prevChampion.Score
		prevName = prevChampion.Name
		if score <= prevScore {
			return &NotHigherScore{
				Score:     score,
				PrevScore: prevScore,
			}
		}
		parent := datastore.NewKey(c, "champions", "_", 0, nil)
		key := datastore.NewIncompleteKey(c, "champion", parent)
		_, err = datastore.Put(c, key, champion)
		return err
	}, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Infof(
		c, "Champion has been beaten: %d by '%s' -> %d by '%s' in %.3f sec",
		prevScore, prevName, score, name, duration,
	)
	WriteAuthorizedChampion(w, champion)
}

// A handler for "PUT /champion" to rename the current record.
func RenameChampion(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	name := r.FormValue("name")
	name = NormalizeName(name)
	log.Debugf(c, "Trying to rename champion: %s", name)
	_, token, _ := r.BasicAuth()
	t := time.Now()
	var _champion Champion
	var prevName string
	err := datastore.RunInTransaction(c, func(c context.Context) error {
		champion, key, err := LoadChampion(c, t, TTL)
		if err != nil {
			return err
		}
		prevName = champion.Name
		if champion.Token != token {
			return &NotAuthorized{}
		}
		champion.Name = name
		_, err = datastore.Put(c, key, champion)
		_champion = *champion
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
	log.Infof(c, "Champion has been renamed: %s -> %s", prevName, name)
	WriteAuthorizedChampion(w, &_champion)
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
