package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth/redact"
	"github.com/LUKER2DW/sentinela/internal/camera/dvrip"
	"github.com/LUKER2DW/sentinela/internal/config"
)

type cameraDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Recording   bool   `json:"recording"`
	RecordAudio bool   `json:"record_audio"`
	Retention   int    `json:"retention_days"`
}

func (s *Server) listCameras(w http.ResponseWriter, r *http.Request) {
	out := make([]cameraDTO, 0, len(s.Cfg.Cameras))
	for _, c := range s.Cfg.Cameras {
		out = append(out, cameraDTO{ID: c.ID, Name: c.Name, Recording: c.Recording(), RecordAudio: c.AudioRecording(), Retention: c.RetentionDays})
	}
	writeJSON(w, http.StatusOK, out)
}

// camErr converts a driver error into a safe message and HTTP status.
func (s *Server) camErr(w http.ResponseWriter, cam *config.Camera, err error) {
	msg := redact.Secrets(err.Error(), cam.Password)
	s.Log.Warn("camera control failed", "camera", cam.ID, "err", msg)
	switch {
	case errors.Is(err, dvrip.ErrInvalidCommand):
		writeErr(w, http.StatusBadRequest, "invalid command")
	case errors.Is(err, dvrip.ErrUnsupportedModel):
		writeErr(w, http.StatusForbidden, "control not approved for this camera model")
	case errors.Is(err, dvrip.ErrRejected):
		writeErr(w, http.StatusBadGateway, "camera rejected the command")
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "camera did not answer in time")
	default:
		writeErr(w, http.StatusBadGateway, "camera unreachable")
	}
}

func (s *Server) cameraControl(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	s.stateMu.Lock()
	c, ok := s.stateTTL[cam.ID]
	s.stateMu.Unlock()
	if ok && time.Since(c.at) < 15*time.Second && r.URL.Query().Get("fresh") == "" {
		writeJSON(w, http.StatusOK, c.st)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	st, err := s.ctrl[cam.ID].State(ctx)
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	s.stateMu.Lock()
	s.stateTTL[cam.ID] = cachedState{at: time.Now(), st: st}
	s.stateMu.Unlock()
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) invalidateState(id string) {
	s.stateMu.Lock()
	delete(s.stateTTL, id)
	s.stateMu.Unlock()
}

func (s *Server) ptz(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	var body struct {
		Action string `json:"action"` // start (also keepalive), stop, nudge
		Op     string `json:"op"`     // move, zoom, focus, iris
		Dir    string `json:"dir"`
		Speed  int    `json:"speed"`
		Ms     int    `json:"ms"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dvrip.DefaultTimeout+time.Second)
	defer cancel()
	ctrl := s.ctrl[cam.ID]
	var err error
	switch body.Action {
	case "start":
		err = ctrl.Start(ctx, body.Op, body.Dir, body.Speed)
	case "stop":
		// Stop must go through even if the client went away.
		ctx2, cancel2 := context.WithTimeout(context.WithoutCancel(r.Context()), dvrip.DefaultTimeout)
		defer cancel2()
		err = ctrl.Stop(ctx2)
	case "nudge":
		err = ctrl.Nudge(ctx, body.Op, body.Dir, body.Speed, time.Duration(body.Ms)*time.Millisecond)
	default:
		err = dvrip.ErrInvalidCommand
	}
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) preset(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	id, err := strconv.Atoi(r.PathValue("preset"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad preset id")
		return
	}
	var body struct {
		Action string `json:"action"` // goto, set, clear
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*dvrip.DefaultTimeout)
	defer cancel()
	err = s.ctrl[cam.ID].Preset(ctx, body.Action, id)
	if body.Action != "goto" {
		s.audit(r, "camera.preset."+body.Action, fmt.Sprintf("%s#%d", cam.ID, id), err == nil, "")
		s.invalidateState(cam.ID)
	}
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) setLight(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*dvrip.DefaultTimeout)
	defer cancel()
	err := s.ctrl[cam.ID].SetWhiteLight(ctx, body.On)
	s.audit(r, "camera.white_light", cam.ID, err == nil, strconv.FormatBool(body.On))
	s.invalidateState(cam.ID)
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) setDayNight(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*dvrip.DefaultTimeout)
	defer cancel()
	err := s.ctrl[cam.ID].SetDayNight(ctx, body.Mode)
	s.audit(r, "camera.day_night", cam.ID, err == nil, body.Mode)
	s.invalidateState(cam.ID)
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
