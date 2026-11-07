package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// FCMClient is the house's Firebase client config: what the generic app
// needs to start Firebase for this house's own project (ADR-0020). These
// values ship inside any Firebase app, so they are not secret; the
// server's service account (the secret half) never leaves the server.
type FCMClient struct {
	ProjectID     string `json:"project_id"`
	AppID         string `json:"app_id"`
	APIKey        string `json:"api_key"`
	SenderID      string `json:"sender_id"`
	StorageBucket string `json:"storage_bucket,omitempty"`
}

// LoadFCMClient reads the house's google-services.json and picks the
// Android client of pkg. A missing file is not an error (nil, nil).
func LoadFCMClient(path, pkg string) (*FCMClient, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fcm client: %w", err)
	}
	return ParseFCMClient(b, pkg)
}

// ParseFCMClient is LoadFCMClient without file I/O.
func ParseFCMClient(b []byte, pkg string) (*FCMClient, error) {
	var gs struct {
		ProjectInfo struct {
			ProjectNumber string `json:"project_number"`
			ProjectID     string `json:"project_id"`
			StorageBucket string `json:"storage_bucket"`
		} `json:"project_info"`
		Client []struct {
			ClientInfo struct {
				AppID   string `json:"mobilesdk_app_id"`
				Android struct {
					Package string `json:"package_name"`
				} `json:"android_client_info"`
			} `json:"client_info"`
			APIKey []struct {
				Key string `json:"current_key"`
			} `json:"api_key"`
		} `json:"client"`
	}
	if err := json.Unmarshal(b, &gs); err != nil {
		return nil, errors.New("fcm client: google-services.json is not JSON")
	}
	for _, c := range gs.Client {
		if c.ClientInfo.Android.Package != pkg || len(c.APIKey) == 0 {
			continue
		}
		out := &FCMClient{ProjectID: gs.ProjectInfo.ProjectID, AppID: c.ClientInfo.AppID, APIKey: c.APIKey[0].Key,
			SenderID: gs.ProjectInfo.ProjectNumber, StorageBucket: gs.ProjectInfo.StorageBucket}
		if out.ProjectID == "" || out.AppID == "" || out.APIKey == "" || out.SenderID == "" {
			return nil, errors.New("fcm client: incomplete Android client")
		}
		return out, nil
	}
	return nil, fmt.Errorf("fcm client: no Android client for %s", pkg)
}
