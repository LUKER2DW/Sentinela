package apps

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Secrets stay encrypted outside project/release directories. The key is a
// private server file, never a camera credential or part of a source archive.
type secretVault struct {
	Vars     []EnvVar `json:"vars"`
	Files    []string `json:"files,omitempty"`
	Previous []string `json:"previous,omitempty"` // redaction of retained Docker logs after rotation
}

func (a *Agent) openVaultKey() error {
	p := filepath.Join(a.Cfg.StateDir, "secrets.key")
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		// Losing the key must never silently create a different key for an
		// existing vault. Keep the encrypted files intact for recovery.
		for _, pattern := range []string{"apps/*/secrets.enc", "uploads/*.secrets.enc"} {
			matches, _ := filepath.Glob(filepath.Join(a.Cfg.StateDir, filepath.FromSlash(pattern)))
			if len(matches) > 0 {
				return errors.New("chave dos secrets ausente; restaure secrets.key do backup do servidor")
			}
		}
		b = make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("criar chave dos secrets: %w", err)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("guardar chave dos secrets: %w", err)
		}
		_, err = f.Write(b)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return fmt.Errorf("guardar chave dos secrets: %w", err)
		}
	} else if err != nil {
		return errors.New("não foi possível abrir a chave dos secrets")
	}
	if len(b) != 32 {
		return errors.New("a chave dos secrets está inválida")
	}
	if err := os.Chmod(p, 0o600); err != nil {
		return errors.New("não foi possível proteger a chave dos secrets")
	}
	block, err := aes.NewCipher(b)
	if err != nil {
		return err
	}
	a.vaultCipher, err = cipher.NewGCM(block)
	return err
}

func (a *Agent) readVault(p, identity string) (secretVault, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return secretVault{}, nil
	}
	if err != nil {
		return secretVault{}, errors.New("não foi possível abrir os secrets do serviço")
	}
	n := a.vaultCipher.NonceSize()
	if len(b) < n {
		return secretVault{}, errors.New("arquivo de secrets inválido")
	}
	plain, err := a.vaultCipher.Open(nil, b[:n], b[n:], []byte(identity))
	if err != nil {
		return secretVault{}, errors.New("não foi possível autenticar os secrets do serviço")
	}
	var v secretVault
	if json.Unmarshal(plain, &v) != nil {
		return secretVault{}, errors.New("arquivo de secrets inválido")
	}
	return v, nil
}

func (a *Agent) writeVault(p, identity string, v secretVault) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	nonce := make([]byte, a.vaultCipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("criar nonce dos secrets: %w", err)
	}
	ciphertext := a.vaultCipher.Seal(nonce, nonce, b, []byte(identity))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("criar pasta dos secrets: %w", err)
	}
	if err := os.WriteFile(p+".tmp", ciphertext, 0o600); err != nil {
		return errors.New("não foi possível guardar os secrets")
	}
	if err := os.Rename(p+".tmp", p); err != nil {
		return errors.New("não foi possível guardar os secrets")
	}
	return nil
}

func (a *Agent) vaultFile(id string) string { return filepath.Join(a.appDir(id), "secrets.enc") }
func (a *Agent) uploadVaultFile(id string) string {
	return filepath.Join(a.Cfg.StateDir, "uploads", id+".secrets.enc")
}

// storeVaultLocked persists before changing in-memory injection state.
func (a *Agent) storeVaultLocked(id string, v secretVault) error {
	v.Previous = slices.Clone(v.Previous)
	for _, e := range a.vaults[id].Vars {
		if e.Value != "" && !slices.Contains(v.Previous, e.Value) {
			v.Previous = append(v.Previous, e.Value)
		}
	}
	if len(v.Previous) > 256 {
		v.Previous = slices.Clone(v.Previous[len(v.Previous)-256:])
	}
	for i := range v.Vars {
		v.Vars[i].Secret = true
	}
	if err := a.writeVault(a.vaultFile(id), id, v); err != nil {
		return err
	}
	a.vaults[id] = v
	if app := a.find(id); app != nil {
		for i := range app.Env {
			if !app.Env[i].Secret {
				continue
			}
			app.Env[i].Value = ""
			for _, e := range v.Vars {
				if e.Key == app.Env[i].Key {
					app.Env[i].Value = e.Value
					break
				}
			}
		}
		if app.Deployed != nil {
			app.Deployed.FileEnv = slices.Clone(v.Vars)
		}
	}
	return nil
}

func mergeVault(v secretVault, vars []EnvVar, files []string, overwrite bool) secretVault {
	v.Vars = slices.Clone(v.Vars)
	v.Files = slices.Clone(v.Files)
	for _, e := range vars {
		e.Secret = true
		i := slices.IndexFunc(v.Vars, func(x EnvVar) bool { return x.Key == e.Key })
		if i < 0 {
			v.Vars = append(v.Vars, e)
		} else if overwrite {
			v.Vars[i] = e
		}
	}
	for _, f := range files {
		if !slices.Contains(v.Files, f) {
			v.Files = append(v.Files, f)
		}
	}
	return v
}

func importableEnv(name string) bool {
	n := strings.ToLower(path.Base(name))
	if !strings.HasPrefix(n, ".env") && !strings.HasSuffix(n, ".env") {
		return false
	}
	for _, word := range []string{"example", "sample", "template"} {
		if strings.Contains(n, word) {
			return false
		}
	}
	return true
}

// collectEnv validates every file before anything is deleted. The canonical
// .env wins over alternative env files. Neither values nor contents are logged.
func collectEnv(ctx context.Context, dir string) (secretVault, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return secretVault{}, err
	}
	defer root.Close()
	var names []string
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if importableEnv(p) {
			if !d.Type().IsRegular() {
				return errors.New("um arquivo .env é um link ou arquivo especial")
			}
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		return secretVault{}, err
	}
	slices.Sort(names)
	// Root config is loaded last; nested examples do not supersede it.
	slices.SortStableFunc(names, func(x, y string) int {
		if x == ".env" {
			return 1
		}
		if y == ".env" {
			return -1
		}
		return 0
	})
	v := secretVault{}
	for _, name := range names {
		b, err := readProjectText(root, name)
		if err != nil {
			return secretVault{}, errors.New("não foi possível ler um arquivo .env do projeto")
		}
		vars, err := parseDotEnv(b)
		if err != nil {
			return secretVault{}, fmt.Errorf("%s: %w", name, err)
		}
		v = mergeVault(v, vars, []string{name}, true)
		if len(v.Vars) > maxEnv {
			return secretVault{}, fmt.Errorf("os arquivos .env somam mais de %d variáveis", maxEnv)
		}
	}
	return v, nil
}

// importEnvDocument handles files sent through the service explorer without
// ever creating a plaintext environment file in the project or data volume.
func (a *Agent) importEnvDocument(id, name string, vars []EnvVar) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.find(id) == nil {
		return errNoApp
	}
	next := mergeVault(a.vaults[id], vars, []string{name}, true)
	if len(next.Vars) > maxEnv {
		return errors.New("limite de 64 secrets atingido")
	}
	if err := a.storeVaultLocked(id, next); err != nil {
		return err
	}
	return a.saveLocked()
}

func removeEnvFiles(dir string, names []string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range names {
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("secrets guardados, mas não foi possível remover um arquivo .env")
		}
	}
	return nil
}

func (a *Agent) absorbEnv(ctx context.Context, id, dir string) error {
	v, err := collectEnv(ctx, dir)
	if err != nil {
		return err
	}
	if len(v.Files) == 0 {
		return nil
	}
	a.mu.Lock()
	previous := a.vaults[id]
	next := mergeVault(previous, v.Vars, v.Files, false) // existing manager/manual values win
	if len(next.Vars) > maxEnv {
		a.mu.Unlock()
		return errors.New("limite de 64 secrets atingido")
	}
	err = a.storeVaultLocked(id, next)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return removeEnvFiles(dir, v.Files)
}

func (a *Agent) hSecrets(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.find(id) == nil {
		reply(w, nil, errNoApp)
		return
	}
	v := a.vaults[id]
	out := make([]EnvVar, len(v.Vars))
	for i, e := range v.Vars {
		out[i] = EnvVar{Key: e.Key, Secret: true}
	}
	b, err := os.ReadFile(a.vaultFile(id))
	if err != nil {
		reply(w, nil, errors.New("não foi possível ler a versão dos secrets"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"env": out, "files": v.Files, "encrypted": true, "revision": fileRevision(b)})
}

func (a *Agent) hSaveSecrets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Env      []EnvVar `json:"env"`
		Revision string   `json:"revision"`
	}
	// 64 values of 8 KiB can occupy over 3 MiB after JSON escaping.
	if !readBodySize(w, r, &body, 4<<20) {
		return
	}
	id := r.PathValue("id")
	a.mu.Lock()
	app := a.find(id)
	if app == nil {
		a.mu.Unlock()
		reply(w, nil, errNoApp)
		return
	}
	if a.busyLocked(a.rt(id)) {
		a.mu.Unlock()
		reply(w, nil, errBusy)
		return
	}
	b, err := os.ReadFile(a.vaultFile(id))
	if err != nil || body.Revision != fileRevision(b) {
		a.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "os secrets mudaram; recarregue o gerenciador antes de salvar"})
		return
	}
	for i := range body.Env {
		body.Env[i].Secret = true
	}
	mergeSecrets(body.Env, a.vaults[id].Vars)
	test := *app
	test.Env = body.Env
	if err := test.Validate(a.limits()); err != nil {
		a.mu.Unlock()
		reply(w, nil, err)
		return
	}
	v := a.vaults[id]
	v.Vars = body.Env
	err = a.storeVaultLocked(id, v)
	// Manager deletions also remove manual secret metadata, so an old empty
	// dashboard variable cannot override or resurrect a deleted secret.
	if err == nil {
		app.Env = slices.DeleteFunc(app.Env, func(e EnvVar) bool {
			return e.Secret && !slices.ContainsFunc(v.Vars, func(x EnvVar) bool { return x.Key == e.Key })
		})
		for i := range app.Env {
			for _, e := range v.Vars {
				if e.Key == app.Env[i].Key {
					app.Env[i] = e
					break
				}
			}
		}
		err = a.saveLocked()
	}
	restart := app.Deployed != nil && app.Desired == Running
	a.mu.Unlock()
	if err == nil && restart {
		err = a.Enqueue(id, "recreate")
	}
	reply(w, map[string]bool{"ok": true, "restarting": restart}, err)
}
