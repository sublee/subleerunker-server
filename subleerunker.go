// A simple HTTP server for world-best-score in SUBLEERUNKER.  It remembers
// a score for about a month.
package subleerunker

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)
import (
	"golang.org/x/net/context"
	"google.golang.org/appengine"
	"google.golang.org/appengine/memcache"
)

func init() {
	fs := http.FileServer(http.Dir("."))
	http.Handle("/", fs)
	http.HandleFunc("/score", score)
}

// Loads the current score from the Memcache.
func loadScore(c context.Context) (int, error) {
	item, err := memcache.Get(c, "score")
	if err == memcache.ErrCacheMiss {
		return 0, nil
	} else if err == nil {
		score, err := strconv.Atoi(string(item.Value))
		if err != nil {
			score = 0
		}
		return score, nil
	} else {
		return -1, err
	}
}

// A handler for "GET /score".
func getScore(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	score, err := loadScore(c)
	if err != nil {
		http.Error(w, "Memcache Failed", http.StatusInternalServerError)
		return
	}
	fmt.Fprint(w, score)
}

// A handler for "PUT /score".
func putScore(w http.ResponseWriter, r *http.Request) {
	score, err := strconv.Atoi(r.FormValue("score"))
	if err != nil {
		http.Error(w, "Invalid Score Input", http.StatusBadRequest)
		return
	}
	c := appengine.NewContext(r)
	prevScore, err := loadScore(c)
	if err != nil {
		http.Error(w, "Memcache Failed", http.StatusInternalServerError)
		return
	}
	if score < prevScore {
		http.Error(w, "Not New Best Score", http.StatusBadRequest)
		return
	}
	item := &memcache.Item{
		Key:        "score",
		Value:      []byte(strconv.Itoa(score)),
		Expiration: 30 * 24 * time.Hour, // 30 days
	}
	err = memcache.Set(c, item)
	if err != nil {
		http.Error(w, "Memcache Failed", http.StatusInternalServerError)
		return
	}
	fmt.Fprint(w, score)
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
