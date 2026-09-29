package api

import (
	"net/http"
	"time"

	"github.com/LUKER2DW/sentinela/internal/review"
)

// reviewItems groups the events of a range (from/to/camera like /api/events,
// default last 7 days) and marks what the user has seen.
func (s *Server) reviewItems(w http.ResponseWriter, r *http.Request) ([]review.Item, bool) {
	q := r.URL.Query()
	to := time.Now().UTC()
	from := to.Add(-7 * 24 * time.Hour)
	if v, err := time.Parse(time.RFC3339, q.Get("from")); err == nil {
		from = v
	}
	if v, err := time.Parse(time.RFC3339, q.Get("to")); err == nil {
		to = v
	}
	cam := q.Get("camera")
	if cam != "" && s.Cfg.Camera(cam) == nil {
		writeErr(w, http.StatusNotFound, "camera not found")
		return nil, false
	}
	// Start a little early so an item that began before `from` is not split.
	ev, err := s.Store.EventsBetween(r.Context(), cam, from.Add(-review.Gap), to, 5000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return nil, false
	}
	items := review.Group(ev, review.Gap, s.hasSnapshot)
	kept := items[:0]
	for _, it := range items {
		if !it.Start.Before(from) {
			kept = append(kept, it)
		}
	}
	ids := make([]int64, len(kept))
	for i, it := range kept {
		ids[i] = it.ID
	}
	seen, err := s.Store.SeenItems(r.Context(), userFrom(r).ID, ids)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return nil, false
	}
	for i := range kept {
		kept[i].Seen = seen[kept[i].ID]
	}
	return kept, true
}

// listReview: GET /api/review?from&to&camera&severity=alert|detection&unseen=1
func (s *Server) listReview(w http.ResponseWriter, r *http.Request) {
	items, ok := s.reviewItems(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	sev, onlyUnseen := q.Get("severity"), q.Get("unseen") == "1"
	out := []review.Item{}
	unseen := 0
	for _, it := range items {
		if !it.Seen {
			unseen++
		}
		if (sev != "" && it.Severity != sev) || (onlyUnseen && it.Seen) {
			continue
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "unseen": unseen})
}

// markSeen: POST /api/review/seen {"ids":[…]} or {"all":true} with the same
// query range as the list ("mark all seen").
func (s *Server) markSeen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
		All bool    `json:"all"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ids := body.IDs
	if body.All {
		items, ok := s.reviewItems(w, r)
		if !ok {
			return
		}
		for _, it := range items {
			if !it.Seen {
				ids = append(ids, it.ID)
			}
		}
	}
	if len(ids) > 5000 {
		writeErr(w, http.StatusBadRequest, "too many items")
		return
	}
	if err := s.Store.MarkSeen(r.Context(), userFrom(r).ID, ids); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"marked": len(ids)})
}
