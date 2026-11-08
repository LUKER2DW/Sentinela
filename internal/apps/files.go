package apps

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxFileUpload = 50 << 20

type projectEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Dir      bool   `json:"dir"`
	Bytes    int64  `json:"bytes"`
	Modified string `json:"modified"`
	Secret   bool   `json:"secret"`
}

type fileDocument struct {
	Content  string   `json:"content,omitempty"`
	Env      []EnvVar `json:"env,omitempty"`
	Kind     string   `json:"kind"`
	Revision string   `json:"revision"`
}

func projectPath(p string, allowRoot bool) (string, error) {
	if p == "" && allowRoot {
		return ".", nil
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") || !validRel(p) || p == "." || strings.Contains(p, ":") {
		return "", errors.New("invalid path inside the service")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == "" || strings.HasPrefix(part, ".sentinela-") {
			return "", errors.New("reserved or invalid path")
		}
	}
	for _, c := range p {
		if c < 32 {
			return "", errors.New("invalid path")
		}
	}
	return p, nil
}

func (a *Agent) fileRoot(r *http.Request, write bool) (*App, *os.Root, string, error) {
	id := r.PathValue("id")
	app := a.snapshot(id)
	if app == nil {
		return nil, nil, "", errNoApp
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "project"
	}
	if scope != "project" && scope != "data" {
		return nil, nil, "", errors.New("invalid file area")
	}
	if scope == "project" && !app.Managed {
		return nil, nil, "", errors.New("deploy a local project or ZIP to open its protected files")
	}
	if write {
		a.mu.Lock()
		busy := a.busyLocked(a.rt(id))
		a.mu.Unlock()
		if busy {
			return nil, nil, "", errBusy
		}
	}
	root, err := os.OpenRoot(filepath.Join(a.appDir(id), scope))
	if err != nil {
		return nil, nil, "", sanitizeFileError(err)
	}
	return app, root, scope, nil
}

func (a *Agent) hFiles(w http.ResponseWriter, r *http.Request) {
	_, root, _, err := a.fileRoot(r, false)
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer root.Close()
	rel, err := projectPath(r.URL.Query().Get("path"), true)
	if err != nil {
		reply(w, nil, err)
		return
	}
	file, err := root.Open(rel)
	if err != nil {
		reply(w, nil, sanitizeFileError(err))
		return
	}
	defer file.Close()
	entries, err := file.ReadDir(-1)
	if err != nil {
		reply(w, nil, sanitizeFileError(err))
		return
	}
	out := []projectEntry{}
	for _, e := range entries {
		if e.Name() == ".git" || strings.HasPrefix(e.Name(), ".sentinela-") || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		st, err := e.Info()
		if err != nil || !st.IsDir() && !st.Mode().IsRegular() {
			continue
		}
		out = append(out, projectEntry{Name: e.Name(), Path: path.Join(rel, e.Name()), Dir: e.IsDir(), Bytes: st.Size(),
			Modified: st.ModTime().UTC().Format("2006-01-02T15:04:05.999999999Z"), Secret: isEnvFile(e.Name())})
	}
	slices.SortFunc(out, func(x, y projectEntry) int {
		if x.Dir != y.Dir {
			if x.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func readProjectText(root *os.Root, rel string) ([]byte, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, sanitizeFileError(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("choose a text file")
	}
	if st.Size() > maxTextFile {
		return nil, errors.New("the editor accepts files up to 1 MB")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxTextFile+1))
	if err != nil {
		return nil, sanitizeFileError(err)
	}
	if len(b) > maxTextFile || !utf8.Valid(b) || bytes.ContainsRune(b, 0) {
		return nil, errors.New("this file is not UTF-8 text or is over 1 MB")
	}
	return b, nil
}

func fileRevision(b []byte) string {
	digest := sha256.Sum256(b)
	return hex.EncodeToString(digest[:])
}

func configSecrets(name string, text []byte) []string {
	var data any
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json":
		_ = json.Unmarshal(text, &data)
	case ".yml", ".yaml":
		_ = yaml.Unmarshal(text, &data)
	}
	var values []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for key, child := range v {
				if s, ok := child.(string); ok && s != "" && sensitiveKey(key) {
					values = append(values, s)
				}
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(data)
	return values
}

func (a *Agent) fileSecrets(app *App) []string {
	values := envSecrets(app.Env)
	if app.Deployed != nil {
		values = append(values, envSecrets(app.Deployed.FileEnv)...)
	}
	a.mu.Lock()
	values = append(values, envSecrets(a.vaults[app.ID].Vars)...)
	a.mu.Unlock()
	if app.Managed {
		work := filepath.Join(a.appDir(app.ID), "project", filepath.FromSlash(app.Dir))
		if env, err := readDotEnv(work); err == nil {
			values = append(values, envSecrets(env)...)
		}
	}
	return values
}

func maskConfig(text string, values []string) (string, map[string]string) {
	// Config serializers can escape quotes/newlines in a credential. Mask
	// the representation present on disk as well as its decoded value.
	values = slices.Clone(values)
	for _, value := range slices.Clone(values) {
		quoted, _ := json.Marshal(value)
		if len(quoted) >= 2 {
			values = append(values, string(quoted[1:len(quoted)-1]))
		}
		values = append(values, strings.ReplaceAll(value, "'", "''"))
	}
	slices.SortFunc(values, func(x, y string) int {
		if len(x) != len(y) {
			return len(y) - len(x)
		}
		return strings.Compare(x, y)
	})
	markers := map[string]string{}
	seen := map[string]bool{}
	var pairs []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		marker := fmt.Sprintf("__SENTINELA_SECRET_%d__", len(markers))
		if strings.Contains(text, value) {
			pairs = append(pairs, value, marker)
			markers[marker] = value
		}
	}
	return strings.NewReplacer(pairs...).Replace(text), markers
}

func (a *Agent) hReadFile(w http.ResponseWriter, r *http.Request) {
	app, root, _, err := a.fileRoot(r, false)
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer root.Close()
	rel, err := projectPath(r.URL.Query().Get("path"), false)
	if err != nil {
		reply(w, nil, err)
		return
	}
	b, err := readProjectText(root, rel)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if bytes.Contains(b, []byte("PRIVATE KEY-----")) {
		reply(w, nil, errors.New("private keys cannot be shown in the editor"))
		return
	}
	doc := fileDocument{Kind: "text", Revision: fileRevision(b)}
	if isEnvFile(rel) {
		doc.Kind = "env"
		doc.Env, err = parseDotEnv(b)
		for i := range doc.Env {
			doc.Env[i].Value = ""
			doc.Env[i].Secret = true
		}
	} else {
		values := append(a.fileSecrets(app), configSecrets(rel, b)...)
		doc.Content, _ = maskConfig(string(b), values)
	}
	reply(w, doc, err)
}

func (a *Agent) markFileChange(id, scope string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	app := a.find(id)
	if app == nil {
		return errNoApp
	}
	if scope == "project" {
		app.NeedsDeploy = true
	}
	app.UpdatedAt = a.now().UTC()
	return a.saveLocked()
}

func atomicProjectWrite(root *os.Root, rel string, data []byte) error {
	tmp := path.Join(path.Dir(rel), ".sentinela-edit-"+randHex())
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return sanitizeFileError(err)
	}
	defer root.Remove(tmp)
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return sanitizeFileError(err)
	}
	// Preserve executability of scripts; never broaden other permissions.
	if st, err := root.Stat(rel); err == nil {
		if err := root.Chmod(tmp, 0o600|st.Mode()&0o111); err != nil {
			return sanitizeFileError(err)
		}
	}
	return sanitizeFileError(root.Rename(tmp, rel))
}

func (a *Agent) hWriteFile(w http.ResponseWriter, r *http.Request) {
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	app, root, scope, err := a.fileRoot(r, true)
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer root.Close()
	rel, err := projectPath(r.URL.Query().Get("path"), false)
	if err != nil {
		reply(w, nil, err)
		return
	}
	var doc fileDocument
	if !readBodySize(w, r, &doc, 4<<20) {
		return
	}
	_, statErr := root.Stat(rel)
	exists := !errors.Is(statErr, os.ErrNotExist)
	var old []byte
	if exists {
		old, err = readProjectText(root, rel)
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	if exists && doc.Revision != fileRevision(old) || !exists && doc.Revision != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the file changed; open it again before saving"})
		return
	}
	content := doc.Content
	if importableEnv(rel) {
		previous, err := parseDotEnv(old)
		if err != nil {
			reply(w, nil, err)
			return
		}
		for i := range previous {
			previous[i].Secret = true
		}
		mergeSecrets(doc.Env, previous)
		var out strings.Builder
		for _, e := range doc.Env {
			if !envKey.MatchString(e.Key) || strings.ContainsRune(e.Value, 0) || len(e.Value) > maxEnvVal {
				reply(w, nil, errors.New("invalid environment variable"))
				return
			}
			out.WriteString(e.Key + "=" + strconv.Quote(e.Value) + "\n")
		}
		content = out.String()
		if _, err := parseDotEnv([]byte(content)); err != nil {
			reply(w, nil, err)
			return
		}
		err = a.importEnvDocument(app.ID, rel, doc.Env)
		if err == nil && exists {
			err = sanitizeFileError(root.Remove(rel))
		}
		reply(w, map[string]bool{"ok": true, "secrets_imported": true}, err)
		return
	} else {
		values := append(a.fileSecrets(app), configSecrets(rel, old)...)
		_, markers := maskConfig(string(old), values)
		var pairs []string
		for marker, value := range markers {
			pairs = append(pairs, marker, value)
		}
		content = strings.NewReplacer(pairs...).Replace(content)
		if strings.Contains(content, "__SENTINELA_SECRET_") {
			reply(w, nil, errors.New("invalid secret marker; open the file again"))
			return
		}
	}
	if len(content) > maxTextFile || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		reply(w, nil, errors.New("invalid text or larger than 1 MB"))
		return
	}
	if err := atomicProjectWrite(root, rel, []byte(content)); err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]bool{"ok": true}, a.markFileChange(app.ID, scope))
}

func (a *Agent) hUploadFile(w http.ResponseWriter, r *http.Request) {
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	app, root, scope, err := a.fileRoot(r, true)
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer root.Close()
	rel, err := projectPath(r.URL.Query().Get("path"), false)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if _, err := root.Lstat(rel); !errors.Is(err, os.ErrNotExist) {
		reply(w, nil, errors.New("a file with this name already exists; use the editor or delete it before uploading"))
		return
	}
	if importableEnv(rel) {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTextFile))
		var vars []EnvVar
		if err == nil {
			vars, err = parseDotEnv(b)
		}
		if err == nil {
			err = a.importEnvDocument(app.ID, rel, vars)
		}
		reply(w, map[string]bool{"ok": true, "secrets_imported": true}, err)
		return
	}
	f, err := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		reply(w, nil, sanitizeFileError(err))
		return
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, maxFileUpload))
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = root.Remove(rel)
		reply(w, nil, errors.New("incomplete upload or file larger than 50 MB"))
		return
	}
	reply(w, map[string]bool{"ok": true}, a.markFileChange(app.ID, scope))
}

func (a *Agent) hMkdir(w http.ResponseWriter, r *http.Request) {
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	app, root, scope, err := a.fileRoot(r, true)
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer root.Close()
	rel, err := projectPath(r.URL.Query().Get("path"), false)
	if err != nil {
		reply(w, nil, err)
		return
	}
	err = sanitizeFileError(root.Mkdir(rel, 0o700))
	if err == nil {
		err = a.markFileChange(app.ID, scope)
	}
	reply(w, map[string]bool{"ok": true}, err)
}

func (a *Agent) hDeleteFile(w http.ResponseWriter, r *http.Request) {
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	app, root, scope, err := a.fileRoot(r, true)
	if err != nil {
		reply(w, nil, err)
		return
	}
	defer root.Close()
	rel, err := projectPath(r.URL.Query().Get("path"), false)
	if err != nil {
		reply(w, nil, err)
		return
	}
	err = sanitizeFileError(root.RemoveAll(rel))
	if err == nil {
		err = a.markFileChange(app.ID, scope)
	}
	reply(w, map[string]bool{"ok": true}, err)
}
