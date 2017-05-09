// A simple HTTP server for world-best-score in SUBLEERUNKER.  It remembers
// a score for about a week.
package subleerunker

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)
import (
	"golang.org/x/net/context"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
)

type Score struct {
	Score     int
	ExpiresAt time.Time
}

type NotHigherScore struct {
	Score     int
	PrevScore int
}

func (n NotHigherScore) Error() string {
	return fmt.Sprintf(
		"score %d is not higher than prev score %d",
		n.Score, n.PrevScore,
	)
}

func init() {
	fs := http.FileServer(http.Dir("."))
	http.Handle("/", fs)
	http.HandleFunc("/score", score)
}

func getKey(c context.Context) *datastore.Key {
	return datastore.NewKey(c, "score", "score", 0, nil)
}

// Loads the current score from the Google Cloud Datastore.
func loadScore(c context.Context, at time.Time) (int, error) {
	key := getKey(c)
	score := new(Score)
	err := datastore.Get(c, key, score)
	if err == datastore.ErrNoSuchEntity {
		return 0, nil
	} else if err != nil {
		return -1, err
	}
	if at.After(score.ExpiresAt) {
		return 0, nil
	} else {
		return score.Score, nil
	}
}

// A handler for "GET /score".
func getScore(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	score, err := loadScore(c, time.Now())
	if err != nil {
		http.Error(w, "Datastore Failed", http.StatusInternalServerError)
		return
	}
	fmt.Fprint(w, score)
}

// A handler for "PUT /score".
func putScore(w http.ResponseWriter, r *http.Request) {
	scoreValue, err := strconv.Atoi(r.FormValue("score"))
	if err != nil {
		http.Error(w, "Invalid Score Input", http.StatusBadRequest)
		return
	}
	c := appengine.NewContext(r)
	err = datastore.RunInTransaction(c, func(c context.Context) error {
		prevScoreValue, err := loadScore(c, time.Now())
		if err != nil {
			return err
		}
		if scoreValue < prevScoreValue {
			return errors.New("not best score")
		}
		key := getKey(c)
		score := &Score{
			Score:     scoreValue,
			ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // after 7 days
		}
		_, err = datastore.Put(c, key, score)
		return err
	}, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprint(w, scoreValue)
}

// A combined handler for every methods of "/score".
func score(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "https://sublee.github.io")
	switch strings.ToUpper(r.Method) {
	case "OPTIONS":
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, OPTIONS")
		w.Header().Set("Access-Control-Max-Age", "86400")
	case "GET":
		getScore(w, r)
	case "PUT":
		putScore(w, r)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
