package push

import "testing"

func TestParseFCMClient(t *testing.T) {
	const gs = `{"project_info":{"project_number":"123456789","project_id":"house-1","storage_bucket":"house-1.appspot.com"},
"client":[{"client_info":{"mobilesdk_app_id":"1:123456789:android:other","android_client_info":{"package_name":"org.other"}},"api_key":[{"current_key":"k0"}]},
{"client_info":{"mobilesdk_app_id":"1:123456789:android:abc","android_client_info":{"package_name":"casa.sentinela.app"}},"api_key":[{"current_key":"AIzaTest"}]}]}`
	c, err := ParseFCMClient([]byte(gs), "casa.sentinela.app")
	if err != nil {
		t.Fatal(err)
	}
	want := FCMClient{ProjectID: "house-1", AppID: "1:123456789:android:abc", APIKey: "AIzaTest", SenderID: "123456789", StorageBucket: "house-1.appspot.com"}
	if *c != want {
		t.Fatalf("got %+v, want %+v", *c, want)
	}
	if _, err := ParseFCMClient([]byte(gs), "casa.missing"); err == nil {
		t.Fatal("a file without this app's client must be refused")
	}
	if _, err := ParseFCMClient([]byte("nope"), "casa.sentinela.app"); err == nil {
		t.Fatal("not JSON must be refused")
	}
}
