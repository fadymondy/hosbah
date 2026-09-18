package dbsvc

import "testing"

func TestS3Vars(t *testing.T) {
	got := map[string]string{}
	for _, v := range s3Vars("s3://app_files:s3cr%2Ft@10.1.2.3:9000/app-files") {
		got[v[0]] = v[1]
	}
	want := map[string]string{"AWS_ENDPOINT_URL": "http://10.1.2.3:9000", "AWS_ACCESS_KEY_ID": "app_files", "AWS_SECRET_ACCESS_KEY": "s3cr/t",
		"AWS_BUCKET": "app-files", "AWS_USE_PATH_STYLE_ENDPOINT": "true"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if s3Vars("not a url") != nil {
		t.Error("expected nil for a bad URL")
	}
}
