package provisioning

// sshInstallCommand is the agent install run over SSH (a new Proxmox guest without cloud-init, or a
// connected server). Unlike the `curl … | sh` shown to users it cannot fail silently: a minimal
// template (Debian's LXC image) has no curl, and in a pipe the missing curl only feeds sh an empty
// script, which "succeeds". So it installs curl when it is missing, downloads the script to a file
// under set -e, and runs that. sudo is "" when the command runs as root, else e.g. "sudo -n ".
func sshInstallCommand(url, sudo string) string {
	return "set -e; " +
		"if ! command -v curl >/dev/null 2>&1; then " + sudo + "sh -c '" +
		"if command -v apt-get >/dev/null 2>&1; then apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates; " +
		"elif command -v dnf >/dev/null 2>&1; then dnf install -y -q curl; " +
		"elif command -v yum >/dev/null 2>&1; then yum install -y -q curl; " +
		"elif command -v apk >/dev/null 2>&1; then apk add -q curl ca-certificates; " +
		"else echo \"curl is missing and no known package manager can install it\" >&2; exit 1; fi'; fi; " +
		"f=$(mktemp); curl -fsSL '" + url + "' -o \"$f\"; " + sudo + "sh \"$f\"; rm -f \"$f\""
}
