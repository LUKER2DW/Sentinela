package apps

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth/redact"
)

type ServiceExit struct {
	At        time.Time `json:"at"`
	Code      int       `json:"code"`
	OOM       bool      `json:"oom"`
	Reason    string    `json:"reason"`
	Container string    `json:"container,omitempty"`
}

type ServiceEvent struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}

type diagnostics struct {
	Job       *Job           `json:"job,omitempty"`
	Events    []ServiceEvent `json:"events"`
	LastExit  *ServiceExit   `json:"last_exit,omitempty"`
	Restarts  int            `json:"restarts"`
	Error     string         `json:"error,omitempty"`
	PauseTill time.Time      `json:"pause_till,omitempty"`
}

func exitReason(st *ContainerState) string {
	if st.OOMKilled {
		return "O limite de memória foi excedido (OOM). Revise o consumo ou aumente o limite em Configurações."
	}
	switch st.ExitCode {
	case 0:
		return "O processo terminou sem erro, mas este serviço deveria continuar rodando. Revise o comando de início."
	case 132:
		return "Instrução de CPU incompatível (SIGILL, código 132). Revise dependências nativas; neste servidor prefira Node.js Debian a Alpine."
	case 139:
		return "Falha de memória do processo (SIGSEGV, código 139). Revise as dependências nativas e os logs."
	case 137:
		return "O processo foi encerrado à força (SIGKILL, código 137). O Docker não informou OOM."
	case 143:
		return "O processo recebeu um pedido de encerramento (SIGTERM, código 143)."
	case 126:
		return "O comando não pôde ser executado (código 126). Confira as permissões do arquivo."
	case 127:
		return "Comando ou executável não encontrado (código 127). Confira o ambiente e a preparação."
	default:
		return fmt.Sprintf("O processo parou com código %d. Confira a saída e os erros abaixo.", st.ExitCode)
	}
}

// redactLocked protects every log/error surface, including setup output and
// the last saved log read after an agent restart. Callers hold a.mu.
func (a *Agent) redactLocked(id, text string) string {
	var secrets []string
	if app := a.find(id); app != nil {
		secrets = append(secrets, envSecrets(app.Env)...)
		if app.Deployed != nil {
			secrets = append(secrets, envSecrets(app.Deployed.FileEnv)...)
		}
	}
	secrets = append(secrets, envSecrets(a.vaults[id].Vars)...)
	secrets = append(secrets, a.vaults[id].Previous...)
	if r := a.run[id]; r != nil {
		secrets = append(secrets, r.secrets...)
	}
	if a.ghToken != "" {
		secrets = append(secrets, a.ghToken)
	}
	for _, value := range slices.Clone(secrets) {
		quoted, _ := json.Marshal(value)
		if len(quoted) >= 2 {
			secrets = append(secrets, string(quoted[1:len(quoted)-1]))
		}
	}
	slices.SortFunc(secrets, func(x, y string) int { return len(y) - len(x) })
	return redact.Secrets(text, secrets...)
}

func (a *Agent) safeText(id, text string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.redactLocked(id, text)
}

func (a *Agent) eventLocked(id, kind, message string) {
	r := a.rt(id)
	r.events = append(r.events, ServiceEvent{At: a.now().UTC(), Kind: kind, Message: a.redactLocked(id, message)})
	if len(r.events) > 40 {
		r.events = slices.Clone(r.events[len(r.events)-40:])
	}
	a.saveDiagnosticsLocked(id)
}

func (a *Agent) observeExitLocked(id, name string, st *ContainerState) {
	if st == nil || st.Running || st.Status == "created" {
		return
	}
	r := a.rt(id)
	if at, ok := r.lastStops[name]; ok && at.Equal(st.FinishedAt) {
		return
	}
	r.lastStops[name] = st.FinishedAt
	at := st.FinishedAt
	if at.IsZero() {
		at = a.now().UTC()
	}
	reason := exitReason(st)
	if st.Error != "" {
		reason += " " + a.redactLocked(id, st.Error)
	}
	r.lastExit = &ServiceExit{At: at, Code: st.ExitCode, OOM: st.OOMKilled, Reason: reason, Container: name}
	a.eventLocked(id, "exit", name+": "+reason)
}

func (a *Agent) diagnosticsFile(id string) string {
	return filepath.Join(a.appDir(id), "diagnostics.json")
}

func (a *Agent) saveDiagnosticsLocked(id string) {
	r := a.rt(id)
	b, err := json.Marshal(diagnostics{Job: r.job, Events: r.events, LastExit: r.lastExit,
		Restarts: r.restartCount, Error: r.err, PauseTill: r.pauseTill})
	if err != nil {
		return
	}
	if err := os.MkdirAll(a.appDir(id), 0o700); err != nil {
		return
	}
	p := a.diagnosticsFile(id)
	if err := os.WriteFile(p+".tmp", b, 0o600); err == nil {
		_ = os.Rename(p+".tmp", p)
	}
}

func (a *Agent) loadDiagnostics(id string) {
	r := a.rt(id)
	b, err := os.ReadFile(a.diagnosticsFile(id))
	var d diagnostics
	if err != nil || json.Unmarshal(b, &d) != nil {
		return
	}
	r.job, r.events, r.lastExit, r.restartCount, r.err, r.pauseTill = d.Job, d.Events, d.LastExit, d.Restarts, d.Error, d.PauseTill
	if r.job != nil && r.job.Finished.IsZero() {
		r.job.Phase, r.job.Finished = "failed", a.now().UTC()
		r.job.Error = "Deploy interrompido pelo reinício do agente. Tente implantar novamente."
		r.err = r.job.Error
	}
	if len(r.events) > 40 {
		r.events = slices.Clone(r.events[len(r.events)-40:])
	}
	if r.lastExit != nil {
		r.lastStops[r.lastExit.Container] = r.lastExit.At
	}
}

func (a *Agent) rememberError(id string, err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	message := a.redactLocked(id, err.Error())
	a.rt(id).err = message
	a.eventLocked(id, "error", message)
	a.mu.Unlock()
	a.Log.Warn("apps: supervise", "app", id, "err", message)
}

// sanitizeFileError keeps content and absolute paths out of editor errors.
func sanitizeFileError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("arquivo ou pasta não encontrado")
	}
	if errors.Is(err, os.ErrPermission) {
		return errors.New("sem permissão para alterar este arquivo")
	}
	if err != nil {
		return errors.New("não foi possível acessar este arquivo dentro do projeto")
	}
	return nil
}

func isEnvFile(name string) bool {
	n := strings.ToLower(filepath.Base(name))
	return strings.HasPrefix(n, ".env") || strings.HasSuffix(n, ".env")
}
