package apps

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const maxTextFile = 1 << 20

var dotenvAssignment = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]{0,63})\s*=\s*(.*)$`)

func sensitiveKey(key string) bool {
	key = strings.ToUpper(key)
	for _, word := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "PRIVATE", "CREDENTIAL", "API_KEY", "PUBLIC_KEY"} {
		if strings.Contains(key, word) {
			return true
		}
	}
	return false
}

// parseDotEnv implements dotenv's literal assignments, comments, export and
// quoted multiline values. It never evaluates shell code or interpolation.
func parseDotEnv(b []byte) ([]EnvVar, error) {
	if len(b) > maxTextFile || bytes.ContainsRune(b, 0) {
		return nil, errors.New(".env is invalid or larger than 1 MB")
	}
	sc := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})))
	sc.Buffer(make([]byte, 4096), maxTextFile)
	var vars []EnvVar
	indices := map[string]int{}
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		m := dotenvAssignment.FindStringSubmatch(text)
		if m == nil {
			return nil, fmt.Errorf(".env: invalid assignment on line %d", line)
		}
		value := m[2]
		if len(value) > 0 && strings.ContainsRune("\"'`", rune(value[0])) {
			quote := value[0]
			value = value[1:]
			for {
				end := -1
				for i := 0; i < len(value); i++ {
					if value[i] == '\\' && quote == '"' {
						i++
						continue
					}
					if value[i] == quote {
						end = i
						break
					}
				}
				if end >= 0 {
					value = value[:end]
					break
				}
				if !sc.Scan() {
					return nil, fmt.Errorf(".env: unclosed quotes on line %d", line)
				}
				line++
				value += "\n" + sc.Text()
				if len(value) > maxEnvVal {
					return nil, fmt.Errorf(".env: valor de %s grande demais", m[1])
				}
			}
			if quote == '"' {
				value = strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\"`, `"`, `\\`, `\`).Replace(value)
			}
		} else {
			value, _, _ = strings.Cut(value, "#")
			value = strings.TrimSpace(value)
		}
		if len(value) > maxEnvVal {
			return nil, fmt.Errorf(".env: valor de %s grande demais", m[1])
		}
		e := EnvVar{Key: m[1], Value: value, Secret: sensitiveKey(m[1])}
		if idx, ok := indices[e.Key]; ok {
			vars[idx] = e
		} else {
			indices[e.Key] = len(vars)
			vars = append(vars, e)
		}
		if len(vars) > maxEnv {
			return nil, fmt.Errorf(".env: at most %d variables", maxEnv)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, errors.New(".env: could not read the file")
	}
	return vars, nil
}

func readDotEnv(work string) ([]EnvVar, error) {
	root, err := os.OpenRoot(work)
	if err != nil {
		return nil, fmt.Errorf("abrir pasta da .env: %w", err)
	}
	defer root.Close()
	f, err := root.Open(".env")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("could not open the .env inside the project")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxTextFile+1))
	if err != nil {
		return nil, errors.New("could not read the .env")
	}
	return parseDotEnv(b)
}

func envSecrets(env []EnvVar) []string {
	var out []string
	for _, e := range env {
		if e.Secret && e.Value != "" {
			out = append(out, e.Value)
		}
	}
	return out
}
