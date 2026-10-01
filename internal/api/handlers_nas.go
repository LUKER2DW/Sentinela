package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/nas"
	"github.com/LUKER2DW/sentinela/internal/sysinfo"
)

// maxChunk bounds one upload request; clients send ≤ 32 MiB (ADR-0012).
const maxChunk = 64 << 20

// inlineTypes may be shown in the browser; everything else is a download.
// Served with a sandbox CSP and nosniff so a user file can never run script
// on the app's origin.
var inlineTypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".ogg": "audio/ogg", ".opus": "audio/ogg", ".wav": "audio/wav",
	".pdf": "application/pdf",
}

func (s *Server) nasAccess(r *http.Request) nas.Access {
	u := userFrom(r)
	return nas.Access{User: u.Username, Admin: u.Role == auth.RoleAdmin, Write: auth.RoleAtLeast(u.Role, auth.RoleOperator)}
}

// nasReady answers 503 when the server has no Nuvem (dev machines).
func (s *Server) nasReady(w http.ResponseWriter) bool {
	if s.nas == nil {
		writeErr(w, http.StatusServiceUnavailable, "Nuvem not available on this server")
		return false
	}
	return true
}

func (s *Server) nasErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, nas.ErrForbidden):
		writeErr(w, http.StatusForbidden, "not allowed")
	case errors.Is(err, nas.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, nas.ErrExists):
		writeErr(w, http.StatusConflict, "a file or folder with this name already exists")
	case errors.Is(err, nas.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid path or name")
	case errors.Is(err, nas.ErrTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, "file too large (max 4 GB)")
	case errors.Is(err, nas.ErrNoThumb):
		writeErr(w, http.StatusNotFound, "no thumbnail")
	default:
		s.Log.Error("nas", "err", err)
		writeErr(w, http.StatusInternalServerError, "file error")
	}
}

func (s *Server) nasList(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	a := s.nasAccess(r)
	p, err := nas.Clean(r.URL.Query().Get("path"))
	if err != nil {
		s.nasErr(w, err)
		return
	}
	entries, err := s.nas.List(a, p)
	if err != nil {
		s.nasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "entries": entries, "user": a.User, "can_write": a.Write, "owners": s.owners(r, p, entries)})
}

// owners says whose each listed item is, for the Nuvem's owner column and
// the avatars on personal folders: a personal folder and everything in it
// belong to that person; in the shared folder, whoever sent or created it.
func (s *Server) owners(r *http.Request, dir string, entries []nas.Entry) map[string]string {
	out := map[string]string{}
	top, _, _ := strings.Cut(dir, "/")
	switch {
	case dir == "":
		users, err := s.Store.ListUsers(r.Context())
		if err != nil {
			return out
		}
		for _, u := range users {
			out[u.Username] = u.Username
		}
		for name := range out {
			if !slices.ContainsFunc(entries, func(e nas.Entry) bool { return e.Name == name }) {
				delete(out, name)
			}
		}
	case top == nas.Shared:
		paths := make([]string, len(entries))
		for i, e := range entries {
			paths[i] = dir + "/" + e.Name
		}
		by, err := s.Store.Authors(r.Context(), paths)
		if err != nil {
			return out
		}
		for _, e := range entries {
			if u := by[dir+"/"+e.Name]; u != "" {
				out[e.Name] = u
			}
		}
	case top != nas.Trash:
		for _, e := range entries {
			out[e.Name] = top
		}
	}
	return out
}

func (s *Server) nasFile(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	f, info, err := s.nas.OpenFile(s.nasAccess(r), r.URL.Query().Get("path"))
	if err != nil {
		s.nasErr(w, err)
		return
	}
	defer f.Close()
	h := w.Header()
	name := info.Name()
	ctype, inline := inlineTypes[strings.ToLower(path.Ext(name))]
	disp := "attachment"
	if inline && r.URL.Query().Get("download") == "" {
		disp = "inline"
	} else if !inline {
		ctype = "application/octet-stream"
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": name}))
	if ctype == "application/pdf" && disp == "inline" {
		// The Nuvem preview shows PDFs in a frame of the app. Browsers refuse
		// to render a PDF in a sandboxed document, so this one type drops the
		// sandbox; the viewer runs PDF scripts isolated from the app's origin
		// (Chrome/Edge PDFium, Firefox pdf.js), and nothing else may load.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
		h.Set("X-Frame-Options", "SAMEORIGIN")
	} else {
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; media-src 'self'; sandbox")
	}
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func (s *Server) nasThumb(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	out, err := s.thumbs.Get(s.nas, s.nasAccess(r), r.URL.Query().Get("path"))
	if err != nil {
		s.nasErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, out)
}

// nasUploadStatus tells a client where to resume an interrupted upload.
func (s *Server) nasUploadStatus(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	n, err := s.nas.PartSize(s.nasAccess(r), r.URL.Query().Get("path"))
	if err != nil {
		s.nasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"offset": n})
}

// nasUpload writes one chunk: PUT ?path=&offset=&total=, body = bytes.
func (s *Server) nasUpload(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	q := r.URL.Query()
	offset, err1 := strconv.ParseInt(q.Get("offset"), 10, 64)
	total, err2 := strconv.ParseInt(q.Get("total"), 10, 64)
	if err1 != nil || err2 != nil || offset < 0 || total < 0 || offset > total {
		writeErr(w, http.StatusBadRequest, "offset and total are required")
		return
	}
	if total > nas.MaxUpload {
		s.nasErr(w, nas.ErrTooLarge)
		return
	}
	if d, err := sysinfo.DiskUsage(s.nasRoot); err == nil && d.Total > 0 && uint64(total-offset) > d.Free {
		writeErr(w, http.StatusInsufficientStorage, "not enough free space in the Nuvem")
		return
	}
	// A stalled phone must not hold the request forever.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Minute))
	body := http.MaxBytesReader(w, r.Body, maxChunk)
	a := s.nasAccess(r)
	p := q.Get("path")
	have, done, err := s.nas.WriteChunk(a, p, offset, total, body)
	if errors.Is(err, nas.ErrOffset) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "upload offset does not match", "offset": have})
		return
	}
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "chunk too large")
			return
		}
		s.nasErr(w, err)
		return
	}
	if done {
		clean, _ := nas.Clean(p)
		s.audit(r, "nas.upload", clean, true, strconv.FormatInt(total, 10)+" bytes")
	}
	writeJSON(w, http.StatusOK, map[string]any{"offset": have, "done": done})
}

// textTypes may be edited in the browser (small, plain text).
var textTypes = regexp.MustCompile(`(?i)\.(txt|md|log|csv|tsv|json|xml|ya?ml|ini|conf|cfg|toml|srt|vtt)$`)

// nasText saves a text file edited in the Nuvem viewer: PUT ?path=, body =
// the new content (≤ 2 MiB, existing plain-text files only).
func (s *Server) nasText(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	p := r.URL.Query().Get("path")
	if !textTypes.MatchString(p) {
		writeErr(w, http.StatusBadRequest, "only plain-text files can be edited")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, nas.MaxText))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "text too large (max 2 MB)")
		return
	}
	err = s.nas.ReplaceText(s.nasAccess(r), p, data)
	clean, _ := nas.Clean(p)
	s.audit(r, "nas.edit", clean, err == nil, strconv.Itoa(len(data))+" bytes")
	if err != nil {
		s.nasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// nasPurge deletes for good from the caller's recycle bin: DELETE
// ?path=.lixeira/<user>/<item>, or ?path=.lixeira/<user> to empty it.
func (s *Server) nasPurge(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	p := r.URL.Query().Get("path")
	err := s.nas.Purge(s.nasAccess(r), p)
	clean, _ := nas.Clean(p)
	s.audit(r, "nas.purge", clean, err == nil, "")
	if err != nil {
		s.nasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) nasMkdir(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.nas.Mkdir(s.nasAccess(r), body.Path)
	s.audit(r, "nas.mkdir", body.Path, err == nil, "")
	if err != nil {
		s.nasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) nasMove(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.nas.Move(s.nasAccess(r), body.From, body.To)
	s.audit(r, "nas.move", body.From+" -> "+body.To, err == nil, "")
	if err != nil {
		s.nasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) nasDelete(w http.ResponseWriter, r *http.Request) {
	if !s.nasReady(w) {
		return
	}
	p := r.URL.Query().Get("path")
	dst, err := s.nas.Delete(s.nasAccess(r), p)
	s.audit(r, "nas.delete", p, err == nil, dst)
	if err != nil {
		s.nasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"trash": dst})
}

// openNAS opens the file API root: the first "nas" volume (ADR-0012).
// Missing on dev machines, where the API answers 503.
func (s *Server) openNAS() {
	for _, v := range s.Cfg.Volumes {
		if v.Kind != "nas" {
			continue
		}
		if st, err := os.Stat(v.Path); err != nil || !st.IsDir() {
			s.Log.Warn("nuvem file API disabled: share not mounted", "path", v.Path)
			return
		}
		share, err := nas.Open(v.Path)
		if err != nil {
			s.Log.Warn("nuvem file API disabled", "err", err)
			return
		}
		s.nas, s.nasRoot = share, v.Path
		s.thumbs = nas.NewThumbs(s.Cfg.DataDir + "/cache/thumbs")
		return
	}
}
