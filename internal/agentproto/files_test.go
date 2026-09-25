package agentproto

import (
	"strings"
	"testing"
)

func TestValidFSPath(t *testing.T) {
	ok := []string{"/", "/etc", "/etc/hostname", "/var/www/app/index.html", "/a"}
	for _, p := range ok {
		if !ValidFSPath(p) {
			t.Errorf("ValidFSPath(%q) = false, want true", p)
		}
	}
	bad := []string{"", "relative", "etc/passwd", "/etc/../etc/passwd", "/etc/./x", "/etc/",
		"/a//b", "/x\x00y", strings.Repeat("/a", MaxFSPath)}
	for _, p := range bad {
		if ValidFSPath(p) {
			t.Errorf("ValidFSPath(%q) = true, want false", p)
		}
	}
}

func TestFSDenied(t *testing.T) {
	denied := []string{"/etc/hosbah", "/etc/hosbah/agent.toml", "/etc/cloudy", "/etc/cloudy/x",
		"/var/lib/hosbah", "/var/lib/hosbah/x", "/var/lib/cloudy", "/var/lib/cloudy/x",
		"/proc", "/proc/1/mem", "/sys", "/sys/class", "/dev", "/dev/sda"}
	for _, p := range denied {
		if !FSDenied(p) {
			t.Errorf("FSDenied(%q) = false, want true", p)
		}
	}
	allowed := []string{"/etc/hostname", "/etc/hosbahish", "/home/deploy/app.env", "/var/log/syslog", "/tmp/x"}
	for _, p := range allowed {
		if FSDenied(p) {
			t.Errorf("FSDenied(%q) = true, want false", p)
		}
	}
}

func TestCheckFSPathTopLevel(t *testing.T) {
	for _, p := range []string{"/", "/etc", "/var", "/usr", "/home"} {
		if err := checkFSPath(p, true); err == nil {
			t.Errorf("checkFSPath(%q, write) = nil, want an error (top-level directory)", p)
		}
		if err := checkFSPath(p, false); err != nil && !FSDenied(p) {
			t.Errorf("checkFSPath(%q, read) = %v, want nil (reading a top-level dir is fine)", p, err)
		}
	}
	for _, p := range []string{"/home/deploy", "/var/www/app", "/etc/hostname"} {
		if err := checkFSPath(p, true); err != nil {
			t.Errorf("checkFSPath(%q, write) = %v, want nil", p, err)
		}
	}
}

func TestFSListParamsValidate(t *testing.T) {
	if err := (FSListParams{Path: "/home"}).Validate(); err != nil {
		t.Fatalf("valid path rejected: %v", err)
	}
	if err := (FSListParams{Path: "relative"}).Validate(); err == nil {
		t.Fatal("relative path accepted")
	}
	if err := (FSListParams{Path: "/etc/hosbah"}).Validate(); err == nil {
		t.Fatal("denylisted path accepted")
	}
}

func TestFSReadParamsValidate(t *testing.T) {
	if err := (FSReadParams{Path: "/home/x", MaxBytes: MaxFSBytes}).Validate(); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	if err := (FSReadParams{Path: "/home/x", MaxBytes: MaxFSBytes + 1}).Validate(); err == nil {
		t.Fatal("over-cap max_bytes accepted")
	}
	if err := (FSReadParams{Path: "/home/x", MaxBytes: -1}).Validate(); err == nil {
		t.Fatal("negative max_bytes accepted")
	}
}

func TestFSWriteParamsValidate(t *testing.T) {
	if err := (FSWriteParams{Path: "/home/x", Content: "aGVsbG8="}).Validate(); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	if err := (FSWriteParams{Path: "/home/x", Content: "not base64!!"}).Validate(); err == nil {
		t.Fatal("bad base64 accepted")
	}
	if err := (FSWriteParams{Path: "/home/x", Content: "aGVsbG8=", Mode: "999"}).Validate(); err == nil {
		t.Fatal("bad mode accepted")
	}
	if err := (FSWriteParams{Path: "/home/x", Content: "aGVsbG8=", Mode: "0644"}).Validate(); err != nil {
		t.Fatalf("valid mode rejected: %v", err)
	}
	if err := (FSWriteParams{Path: "/etc", Content: "aGVsbG8="}).Validate(); err == nil {
		t.Fatal("write onto a top-level directory accepted")
	}
	if err := (FSWriteParams{Path: "/etc/hosbah/x", Content: "aGVsbG8="}).Validate(); err == nil {
		t.Fatal("write into the denylist accepted")
	}
}

func TestFSDeleteMkdirValidate(t *testing.T) {
	if err := (FSMkdirParams{Path: "/home/new"}).Validate(); err != nil {
		t.Fatalf("valid mkdir rejected: %v", err)
	}
	if err := (FSMkdirParams{Path: "/"}).Validate(); err == nil {
		t.Fatal("mkdir of / accepted")
	}
	if err := (FSDeleteParams{Path: "/home/x", Recursive: true}).Validate(); err != nil {
		t.Fatalf("valid delete rejected: %v", err)
	}
	if err := (FSDeleteParams{Path: "/var/lib/hosbah/x"}).Validate(); err == nil {
		t.Fatal("delete inside the denylist accepted")
	}
}

func TestFSRenameParamsValidate(t *testing.T) {
	if err := (FSRenameParams{From: "/home/a", To: "/home/b"}).Validate(); err != nil {
		t.Fatalf("valid rename rejected: %v", err)
	}
	if err := (FSRenameParams{From: "/home/a", To: "/home/a"}).Validate(); err == nil {
		t.Fatal("rename onto itself accepted")
	}
	if err := (FSRenameParams{From: "/etc/hosbah/x", To: "/home/a"}).Validate(); err == nil {
		t.Fatal("rename out of the denylist accepted")
	}
	if err := (FSRenameParams{From: "/home/a", To: "/etc"}).Validate(); err == nil {
		t.Fatal("rename onto a top-level directory accepted")
	}
}
