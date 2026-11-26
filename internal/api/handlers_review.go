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
	if cam != "" && (s.Cfg.Camera(cam) == nil || !s.canSee(r, cam)) {
		writeErr(w, http.StatusNotFound, "camera not found")
		return nil, false
	}
	// Start a little early so an item that began before `from` is not split.
	ev, err := s.Store.EventsBetween(r.Context(), cam, from.Add(-review.Gap), to, 5000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return nil, false
	}
	items := review.Group(s.visibleEvents(r, ev), review.Gap, s.hasSnapshot)
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

// maxSeenIDs bounds one "ids" call (the page marks one item at a time);
// "all" adds at most what the list shows (reviewItems reads 5000 events).
const maxSeenIDs = 500

// markSeen: POST /api/review/seen {"ids":[…]} or {"all":true} with the same
// query range as the list ("mark all seen"). Only items that exist and that
// the user may see are stored, so the marks stay bounded by the events.
func (s *Server) markSeen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
		All bool    `json:"all"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.IDs) > maxSeenIDs {
		writeErr(w, http.StatusBadRequest, "too many items")
		return
	}
	ids, err := s.visibleEventIDs(r, body.IDs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
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
	if err := s.Store.MarkSeen(r.Context(), userFrom(r).ID, ids); err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"marked": len(ids)})
}

// visibleEventIDs keeps the ids (once each) of existing events of cameras
// the user may see; anything else would be a mark for nothing.
func (s *Server) visibleEventIDs(r *http.Request, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	cams, err := s.Store.EventCameras(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(cams))
	for _, id := range ids {
		if cam, ok := cams[id]; ok && s.canSee(r, cam) {
			out = append(out, id)
			delete(cams, id)
		}
	}
	return out, nil
}
