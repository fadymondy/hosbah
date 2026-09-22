package providers

import (
	"strconv"
	"strings"
)

// GuestUser is the sudo user Hosbah creates on a guest it provisions (and bootstraps over SSH). Guests
// provisioned before the rename have "cloudy"; nothing logs in as it after their agent enrolled.
const GuestUser = "hosbah"

// AgentUserData returns a minimal #cloud-config document that creates a passwordless-sudo GuestUser
// holding sshPublicKeys, skips the package-index refresh (the install command does what it
// needs), and runs installCommand once, on first boot, through sh -c.
//
// Every scalar is emitted as a YAML double-quoted string. strconv.Quote's escapes (\" \\ \n \t \xNN
// \uNNNN ...) are all valid YAML double-quoted escapes, so arbitrary input cannot break the
// document's structure.
func AgentUserData(installCommand string, sshPublicKeys []string) string {
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	b.WriteString("users:\n")
	b.WriteString("  - name: " + GuestUser + "\n")
	b.WriteString("    groups: [sudo]\n")
	b.WriteString("    shell: /bin/bash\n")
	b.WriteString("    sudo: \"ALL=(ALL) NOPASSWD:ALL\"\n")
	b.WriteString("    lock_passwd: true\n")
	var keys []string
	for _, k := range sshPublicKeys {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) > 0 {
		b.WriteString("    ssh_authorized_keys:\n")
		for _, k := range keys {
			b.WriteString("      - " + strconv.Quote(k) + "\n")
		}
	}
	b.WriteString("package_update: false\n")
	if cmd := strings.TrimSpace(installCommand); cmd != "" {
		// runcmd entries run once per instance, which is exactly "install on first boot".
		b.WriteString("runcmd:\n")
		b.WriteString("  - [\"sh\", \"-c\", " + strconv.Quote(cmd) + "]\n")
	}
	return b.String()
}
