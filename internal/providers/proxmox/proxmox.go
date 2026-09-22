package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/fadymondy/hosbah/internal/providers"
)

// Key is the provider key this driver registers under.
const Key = "proxmox"

func init() {
	providers.Register(Key, func(cred providers.Credential) (providers.Provider, error) {
		d, err := New(cred)
		if err != nil {
			return nil, err // never a typed-nil *Driver inside the interface
		}
		return d, nil
	})
}

// Driver is a Proxmox VE provider bound to one API token.
type Driver struct {
	c            *client
	node         string
	storage      string
	bridge       string
	bridgeSet    bool // net0 is only rewritten on clones when the owner chose a bridge explicitly
	pool         string
	templateVMID int
	lxcTemplate  string
	lxcFeatures  string // features for new LXCs; "nesting=1,keyctl=1" by default (keyctl needs a root@pam token)
	snippets     SnippetStore
}

var (
	_ providers.Provider         = (*Driver)(nil)
	_ providers.UserDataDelivery = (*Driver)(nil)
)

// New builds a driver from cred. When snippets_storage and snippets_dir are both set it uses a
// DirSnippetStore; otherwise VM user-data is not delivered (see the package documentation).
func New(cred providers.Credential) (*Driver, error) {
	var store SnippetStore
	if st, dir := cred.Config["snippets_storage"], cred.Config["snippets_dir"]; st != "" && dir != "" {
		store = DirSnippetStore{Dir: dir, Storage: st}
	} else if st != "" || dir != "" {
		return nil, errors.New("proxmox: snippets_storage and snippets_dir must be set together")
	}
	return NewWithSnippetStore(cred, store)
}

// NewWithSnippetStore builds a driver that writes VM user-data through store (nil: no user-data
// delivery). Use it for stores other than a mounted directory.
func NewWithSnippetStore(cred providers.Credential, store SnippetStore) (*Driver, error) {
	cfg := func(k string) string { return strings.TrimSpace(cred.Config[k]) }
	tokenID := cfg("token_id")
	if at, bang := strings.Index(tokenID, "@"), strings.Index(tokenID, "!"); at < 1 || bang < at+2 || bang == len(tokenID)-1 {
		return nil, errors.New("proxmox: token_id must look like USER@REALM!TOKENID (e.g. hosbah@pve!ctl)")
	}
	if cred.Secret == "" {
		return nil, errors.New("proxmox: the token secret is empty")
	}
	for _, k := range []string{"api_url", "node", "storage"} {
		if cfg(k) == "" {
			return nil, fmt.Errorf("proxmox: config %q is required", k)
		}
	}
	fp := cfg("tls_fingerprint")
	insecure := cfg("insecure_tls") == "true"
	if fp != "" && insecure {
		return nil, errors.New("proxmox: set tls_fingerprint or insecure_tls, not both")
	}
	c, err := newClient(cfg("api_url"), tokenID, cred.Secret, fp, insecure)
	if err != nil {
		return nil, err
	}
	d := &Driver{
		c:           c,
		node:        cfg("node"),
		storage:     cfg("storage"),
		bridge:      cfg("bridge"),
		bridgeSet:   cfg("bridge") != "",
		pool:        cfg("pool"),
		lxcTemplate: cfg("lxc_template"),
		lxcFeatures: cfg("lxc_features"),
		snippets:    store,
	}
	if !d.bridgeSet {
		d.bridge = "vmbr0"
	}
	if v := cfg("template_vmid"); v != "" {
		if d.templateVMID, err = strconv.Atoi(v); err != nil || d.templateVMID < 100 {
			return nil, fmt.Errorf("proxmox: template_vmid %q is not a VMID", v)
		}
	}
	return d, nil
}

// Key implements providers.Provider.
func (d *Driver) Key() string { return Key }

// Capabilities implements providers.Provider.
func (d *Driver) Capabilities() providers.Caps { return providers.Caps{Create: true, Power: true} }

// DeliversUserData implements providers.UserDataDelivery: only VMs, and only with a snippet store.
func (d *Driver) DeliversUserData(kind string) bool {
	return kind == providers.KindVM && d.snippets != nil
}

// Validate checks the token works and can see the configured node.
func (d *Driver) Validate(ctx context.Context) error {
	var v struct {
		Version string `json:"version"`
	}
	if err := d.c.do(ctx, http.MethodGet, "/version", nil, &v); err != nil {
		return err
	}
	if err := d.c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(d.node)+"/status", nil, nil); err != nil {
		return fmt.Errorf("proxmox: node %q: %w", d.node, err)
	}
	return nil
}

// Regions lists the cluster's nodes.
func (d *Driver) Regions(ctx context.Context) ([]providers.Region, error) {
	var nodes []struct{ Node, Status string }
	if err := d.c.do(ctx, http.MethodGet, "/nodes", nil, &nodes); err != nil {
		return nil, err
	}
	out := make([]providers.Region, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, providers.Region{ID: n.Node, Name: n.Node, Status: n.Status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

var sizes = []providers.Size{
	{ID: "small", Name: "Small", CPUs: 1, MemoryMB: 1024, DiskGB: 10},
	{ID: "medium", Name: "Medium", CPUs: 2, MemoryMB: 2048, DiskGB: 20},
	{ID: "large", Name: "Large", CPUs: 4, MemoryMB: 4096, DiskGB: 40},
}

// Sizes returns static presets; Proxmox has no machine types. CreateSpec.Options can override them.
func (d *Driver) Sizes(context.Context, string) ([]providers.Size, error) {
	return append([]providers.Size(nil), sizes...), nil
}

// Images lists VM templates ("vm:<vmid>") and LXC templates ("lxc:<volid>") on region's node.
func (d *Driver) Images(ctx context.Context, region string) ([]providers.Image, error) {
	node := d.nodeFor(region)
	var guests []struct {
		VMID     flexInt `json:"vmid"`
		Name     string  `json:"name"`
		Template flexInt `json:"template"`
	}
	if err := d.c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/qemu", nil, &guests); err != nil {
		return nil, err
	}
	var out []providers.Image
	for _, g := range guests {
		if g.Template == 1 {
			out = append(out, providers.Image{ID: "vm:" + strconv.Itoa(int(g.VMID)), Name: g.Name, Kind: providers.KindVM})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	// LXC templates usually live on "local" (or wherever lxc_template points), not on the guest-disk
	// storage. Only the configured storage is required to answer; the others are best effort.
	stores := []string{d.storage}
	if st, _, ok := strings.Cut(d.lxcTemplate, ":"); ok {
		stores = append(stores, st)
	}
	stores = append(stores, "local")
	seenStore, seenVol := map[string]bool{}, map[string]bool{}
	var lxc []providers.Image
	for i, st := range stores {
		if seenStore[st] {
			continue
		}
		seenStore[st] = true
		var content []struct {
			Volid string `json:"volid"`
		}
		path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(st) + "/content"
		if err := d.c.do(ctx, http.MethodGet, path, url.Values{"content": {"vztmpl"}}, &content); err != nil {
			if i == 0 || ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		for _, c := range content {
			if seenVol[c.Volid] {
				continue
			}
			seenVol[c.Volid] = true
			name := c.Volid
			if _, after, ok := strings.Cut(name, "vztmpl/"); ok {
				name = after
			}
			lxc = append(lxc, providers.Image{ID: "lxc:" + c.Volid, Name: name, Kind: providers.KindLXC})
		}
	}
	sort.Slice(lxc, func(i, j int) bool { return lxc[i].ID < lxc[j].ID })
	return append(out, lxc...), nil
}

// EnsureSSHKey returns the key's SHA-256 fingerprint. Proxmox has no key registry; keys are put on
// each guest through CreateSpec.SSHPublicKeys.
func (d *Driver) EnsureSSHKey(_ context.Context, pub string) (string, error) {
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil {
		return "", fmt.Errorf("proxmox: not an SSH public key: %w", err)
	}
	return ssh.FingerprintSHA256(k), nil
}

// Create makes a VM (full clone of the template) or an unprivileged LXC and starts it. If a step
// fails after the guest exists, Create destroys it best-effort and returns the error (with the id,
// so a caller can retry the delete if the cleanup failed too).
func (d *Driver) Create(ctx context.Context, s providers.CreateSpec) (string, error) {
	kind, image, err := d.resolveImage(s)
	if err != nil {
		return "", err
	}
	size, err := resolveSize(s.Size, s.Options)
	if err != nil {
		return "", err
	}
	netCfg, err := parseIPConfig(s.Options["ipconfig0"])
	if err != nil {
		return "", err
	}
	node := d.nodeFor(s.Region)
	var next flexInt
	if err := d.c.do(ctx, http.MethodGet, "/cluster/nextid", nil, &next); err != nil {
		return "", err
	}
	vmid := int(next)
	name := hostname(s.Name, vmid)

	typ := "lxc"
	if kind == providers.KindVM {
		typ = "qemu"
	}
	id := node + "/" + typ + "/" + strconv.Itoa(vmid)
	if kind == providers.KindVM {
		err = d.createVM(ctx, node, vmid, name, image, size, netCfg, s)
	} else {
		err = d.createLXC(ctx, node, vmid, name, image, size, netCfg, s)
	}
	if err == nil {
		return id, nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if derr := d.Delete(cleanup, id); derr != nil {
		return id, fmt.Errorf("%w (and removing the half-created guest %s failed: %v)", err, id, derr)
	}
	return "", err
}

func (d *Driver) createVM(ctx context.Context, node string, vmid int, name, template string, size providers.Size, netCfg string, s providers.CreateSpec) error {
	n, v := url.PathEscape(node), strconv.Itoa(vmid)
	clone := url.Values{"newid": {v}, "name": {name}, "full": {"1"}, "storage": {d.storage}}
	if d.pool != "" {
		clone.Set("pool", d.pool)
	}
	if err := d.c.startTask(ctx, http.MethodPost, "/nodes/"+n+"/qemu/"+url.PathEscape(template)+"/clone", clone); err != nil {
		return err
	}

	conf := url.Values{
		"cores":     {strconv.Itoa(size.CPUs)},
		"memory":    {strconv.Itoa(size.MemoryMB)},
		"ciuser":    {providers.GuestUser},
		"ipconfig0": {netCfg},
	}
	if keys := joinKeys(s.SSHPublicKeys); keys != "" {
		// qemu-server declares sshkeys as "urlencoded" and runs uri_unescape over it, which decodes
		// %XX only — '+' is kept literally. So the value is percent-encoded with spaces as %20 (never
		// '+'), and that string is then form-encoded like any other parameter.
		conf.Set("sshkeys", pveURLEncode(keys))
	}
	if d.bridgeSet {
		conf.Set("net0", "virtio,bridge="+d.bridge)
	}
	if tags := pveTags(s.Tags); tags != "" {
		conf.Set("tags", tags)
	}
	if s.UserData != "" && d.snippets != nil {
		volid, err := d.snippets.Put(ctx, snippetFile(vmid), []byte(s.UserData))
		if err != nil {
			return err
		}
		conf.Set("cicustom", "user="+volid)
	}
	base := "/nodes/" + n + "/qemu/" + v
	if err := d.c.startTask(ctx, http.MethodPost, base+"/config", conf); err != nil {
		return err
	}

	// Grow scsi0 to the requested size. Proxmox refuses to shrink, so a template disk that is already
	// larger is left alone.
	var cur struct {
		SCSI0 string `json:"scsi0"`
	}
	if err := d.c.do(ctx, http.MethodGet, base+"/config", nil, &cur); err != nil {
		return err
	}
	if have, ok := diskSizeGB(cur.SCSI0); ok && float64(size.DiskGB) > have {
		resize := url.Values{"disk": {"scsi0"}, "size": {strconv.Itoa(size.DiskGB) + "G"}}
		if err := d.c.startTask(ctx, http.MethodPut, base+"/resize", resize); err != nil {
			return err
		}
	}
	return d.c.startTask(ctx, http.MethodPost, base+"/status/start", nil)
}

func (d *Driver) createLXC(ctx context.Context, node string, vmid int, name, template string, size providers.Size, netCfg string, s providers.CreateSpec) error {
	features := "nesting=1,keyctl=1" // Docker in an unprivileged container wants both
	if d.lxcFeatures != "" {
		features = d.lxcFeatures // a non-root token may only set nesting (Proxmox reserves the rest for root@pam)
	}
	if f := s.Options["features"]; f != "" {
		features = f
	}
	p := url.Values{
		"vmid":         {strconv.Itoa(vmid)},
		"hostname":     {name},
		"ostemplate":   {template},
		"storage":      {d.storage},
		"rootfs":       {d.storage + ":" + strconv.Itoa(size.DiskGB)},
		"cores":        {strconv.Itoa(size.CPUs)},
		"memory":       {strconv.Itoa(size.MemoryMB)},
		"swap":         {"512"},
		"unprivileged": {"1"},
		"features":     {features},
		"net0":         {"name=eth0,bridge=" + d.bridge + "," + netCfg},
		"start":        {"1"},
	}
	if keys := joinKeys(s.SSHPublicKeys); keys != "" {
		p.Set("ssh-public-keys", keys) // plain text here, unlike qemu's sshkeys
	}
	if ns := strings.TrimSpace(s.Options["nameserver"]); ns != "" {
		p.Set("nameserver", ns) // a static address usually needs its own resolver (the host's may be unreachable)
	}
	if d.pool != "" {
		p.Set("pool", d.pool)
	}
	if err := d.c.startTask(ctx, http.MethodPost, "/nodes/"+url.PathEscape(node)+"/lxc", p); err != nil {
		return err
	}
	// Tags after the fact: at create time Proxmox checks them against /vms/<id>, which a pool-scoped
	// token only gets once the guest is in the pool. Best effort: a tag is not worth failing over.
	if tags := pveTags(s.Tags); tags != "" {
		_ = d.c.do(ctx, http.MethodPut, "/nodes/"+url.PathEscape(node)+"/lxc/"+strconv.Itoa(vmid)+"/config", url.Values{"tags": {tags}}, nil)
	}
	return nil
}

// Get reports a guest's status and, when it is running, its addresses. A VM's addresses come from
// the QEMU guest agent; when the agent is not (yet) running the machine simply has no IP.
func (d *Driver) Get(ctx context.Context, id string) (providers.Machine, error) {
	node, typ, vmid, err := parseID(id)
	if err != nil {
		return providers.Machine{}, err
	}
	base := "/nodes/" + url.PathEscape(node) + "/" + typ + "/" + vmid
	var st struct {
		Status string `json:"status"`
		Name   string `json:"name"`
		Lock   string `json:"lock"`
	}
	if err := d.c.do(ctx, http.MethodGet, base+"/status/current", nil, &st); err != nil {
		return providers.Machine{}, err
	}
	m := providers.Machine{ID: id, Name: st.Name, Status: providers.StatusUnknown}
	switch {
	case st.Lock == "create" || st.Lock == "clone":
		m.Status = providers.StatusCreating
	case st.Status == "running":
		m.Status = providers.StatusRunning
	case st.Status == "stopped":
		m.Status = providers.StatusStopped
	}
	if m.Status != providers.StatusRunning {
		return m, nil
	}

	var addrs []ifaceAddr
	if typ == "lxc" {
		var ifs []struct {
			Name  string `json:"name"`
			Inet  string `json:"inet"`
			Inet6 string `json:"inet6"`
		}
		if err := d.c.do(ctx, http.MethodGet, base+"/interfaces", nil, &ifs); err != nil {
			if ctx.Err() != nil {
				return m, err
			}
			return m, nil // interface listing is informational; the guest itself is fine
		}
		for _, i := range ifs {
			addrs = append(addrs, ifaceAddr{i.Name, i.Inet}, ifaceAddr{i.Name, i.Inet6})
		}
	} else {
		var res struct {
			Result []struct {
				Name string `json:"name"`
				IPs  []struct {
					Addr string `json:"ip-address"`
				} `json:"ip-addresses"`
			} `json:"result"`
		}
		if err := d.c.do(ctx, http.MethodGet, base+"/agent/network-get-interfaces", nil, &res); err != nil {
			if ctx.Err() != nil {
				return m, err
			}
			return m, nil // "QEMU guest agent is not running" while the guest boots
		}
		for _, i := range res.Result {
			for _, a := range i.IPs {
				addrs = append(addrs, ifaceAddr{i.Name, a.Addr})
			}
		}
	}
	m.IPv4, m.IPv6, m.PrivateIP = pickIPs(addrs)
	return m, nil
}

// Delete stops the guest if it is running and destroys it with its disks. A guest that is already
// gone counts as deleted.
func (d *Driver) Delete(ctx context.Context, id string) error {
	node, typ, vmid, err := parseID(id)
	if err != nil {
		return err
	}
	base := "/nodes/" + url.PathEscape(node) + "/" + typ + "/" + vmid
	var st struct {
		Status string `json:"status"`
	}
	if err := d.c.do(ctx, http.MethodGet, base+"/status/current", nil, &st); err != nil {
		if isNotFound(err) {
			return nil
		}
		// With a pool-scoped token every guest Hosbah creates is in the pool and visible. A guest it
		// may not even audit was never created by it (a create refused before allocation, say): gone.
		if d.pool != "" && invisibleGuest(err) {
			return nil
		}
		return err
	}
	if st.Status == "running" {
		if err := d.c.startTask(ctx, http.MethodPost, base+"/status/stop", nil); err != nil && !isNotFound(err) {
			return err
		}
	}
	del := url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}
	if err := d.c.startTask(ctx, http.MethodDelete, base, del); err != nil && !isNotFound(err) {
		return err
	}
	if r, ok := d.snippets.(snippetRemover); ok && typ == "qemu" {
		n, _ := strconv.Atoi(vmid)
		for _, name := range []string{snippetFile(n), legacySnippetFile(n)} {
			if err := r.Remove(ctx, name); err != nil {
				return fmt.Errorf("proxmox: guest %s deleted, but its snippet was not: %w", id, err)
			}
		}
	}
	return nil
}

// Power runs a power action and waits for it.
func (d *Driver) Power(ctx context.Context, id string, a providers.PowerAction) error {
	switch a {
	case providers.PowerStart, providers.PowerStop, providers.PowerShutdown, providers.PowerReboot:
	default:
		return fmt.Errorf("proxmox: unknown power action %q", a)
	}
	node, typ, vmid, err := parseID(id)
	if err != nil {
		return err
	}
	return d.c.startTask(ctx, http.MethodPost, "/nodes/"+url.PathEscape(node)+"/"+typ+"/"+vmid+"/status/"+string(a), nil)
}

func (d *Driver) nodeFor(region string) string {
	if region != "" {
		return region
	}
	return d.node
}

// resolveImage decides the kind and the template: an explicit "vm:<vmid>" / "lxc:<volid>" image
// wins, otherwise the configured template for s.Kind.
func (d *Driver) resolveImage(s providers.CreateSpec) (kind, template string, err error) {
	kind = s.Kind
	if pfx, ref, ok := strings.Cut(s.Image, ":"); ok && (pfx == providers.KindVM || pfx == providers.KindLXC) {
		if kind != "" && kind != pfx {
			return "", "", fmt.Errorf("proxmox: image %q does not match kind %q", s.Image, kind)
		}
		kind, template = pfx, ref
	} else if s.Image != "" {
		return "", "", fmt.Errorf("proxmox: image %q must be vm:<vmid> or lxc:<volid>", s.Image)
	}
	switch kind {
	case providers.KindVM:
		if template == "" && d.templateVMID != 0 {
			template = strconv.Itoa(d.templateVMID)
		}
		if _, err := strconv.Atoi(template); err != nil {
			return "", "", errors.New("proxmox: no VM template: set template_vmid or pass image vm:<vmid>")
		}
	case providers.KindLXC:
		if template == "" {
			template = d.lxcTemplate
		}
		if template == "" {
			return "", "", errors.New("proxmox: no LXC template: set lxc_template or pass image lxc:<volid>")
		}
	default:
		return "", "", fmt.Errorf("proxmox: kind must be %q or %q", providers.KindVM, providers.KindLXC)
	}
	return kind, template, nil
}

// resolveSize takes a preset ("" means small) and applies cores / memory_mb / disk_gb overrides.
func resolveSize(id string, opts map[string]string) (providers.Size, error) {
	if id == "" {
		id = sizes[0].ID
	}
	var size providers.Size
	for _, s := range sizes {
		if s.ID == id {
			size = s
		}
	}
	if size.ID == "" {
		return size, fmt.Errorf("proxmox: unknown size %q", id)
	}
	for key, dst := range map[string]*int{"cores": &size.CPUs, "memory_mb": &size.MemoryMB, "disk_gb": &size.DiskGB} {
		v, ok := opts[key]
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return size, fmt.Errorf("proxmox: option %s=%q must be a positive integer", key, v)
		}
		*dst = n
	}
	return size, nil
}

// parseIPConfig validates an ipconfig0 value ("" means ip=dhcp). Its keys (ip, gw, ip6, gw6) mean
// the same in qemu's ipconfig0 and in an LXC net0, so one value serves both kinds.
func parseIPConfig(v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "ip=dhcp", nil
	}
	var parts []string
	for _, kv := range strings.Split(v, ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(kv), "=")
		switch {
		case !ok || val == "":
			return "", fmt.Errorf("proxmox: ipconfig0 %q: %q is not key=value", v, kv)
		case k != "ip" && k != "gw" && k != "ip6" && k != "gw6":
			return "", fmt.Errorf("proxmox: ipconfig0 %q: unsupported key %q (want ip, gw, ip6, gw6)", v, k)
		case strings.ContainsAny(val, " \t\n"):
			return "", fmt.Errorf("proxmox: ipconfig0 %q: bad value for %s", v, k)
		}
		parts = append(parts, k+"="+val)
	}
	return strings.Join(parts, ","), nil
}

// parseID splits "<node>/<qemu|lxc>/<vmid>".
func parseID(id string) (node, typ, vmid string, err error) {
	p := strings.Split(id, "/")
	if len(p) != 3 || p[0] == "" || (p[1] != "qemu" && p[1] != "lxc") {
		return "", "", "", fmt.Errorf("proxmox: bad provider id %q (want <node>/<qemu|lxc>/<vmid>)", id)
	}
	if n, err := strconv.Atoi(p[2]); err != nil || n < 1 {
		return "", "", "", fmt.Errorf("proxmox: bad provider id %q (vmid)", id)
	}
	return p[0], p[1], p[2], nil
}

// hostname turns a display name into a DNS label, as Proxmox requires for both VM names and LXC
// hostnames, falling back to hosbah-<vmid>.
func hostname(name string, vmid int) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	h := strings.Trim(b.String(), "-")
	if len(h) > 63 {
		h = strings.TrimRight(h[:63], "-")
	}
	if h == "" || h[0] >= '0' && h[0] <= '9' {
		return "hosbah-" + strconv.Itoa(vmid)
	}
	return h
}

// pveTags keeps tags Proxmox accepts (lowercase [a-z0-9_+.-]) and joins them with ';'.
func pveTags(tags []string) string {
	var out []string
	for _, t := range tags {
		t = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("_+.-", r) {
				return r
			}
			return -1
		}, strings.ToLower(t))
		if t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, ";")
}

func joinKeys(keys []string) string {
	var out []string
	for _, k := range keys {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return strings.Join(out, "\n")
}

// pveURLEncode percent-encodes s for PVE's uri_unescape: QueryEscape escapes '+' as %2B, so the
// only '+' left in its output are spaces, which become %20.
func pveURLEncode(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

func snippetFile(vmid int) string { return "hosbah-" + strconv.Itoa(vmid) + ".yaml" }

// legacySnippetFile is a guest's snippet as it was named before the rename (removed on delete too).
func legacySnippetFile(vmid int) string { return "cloudy-" + strconv.Itoa(vmid) + ".yaml" }

// diskSizeGB reads the size= of a disk spec such as "local-lvm:vm-101-disk-0,size=3584M".
func diskSizeGB(spec string) (float64, bool) {
	for _, kv := range strings.Split(spec, ",") {
		v, ok := strings.CutPrefix(kv, "size=")
		if !ok || v == "" {
			continue
		}
		mult := map[byte]float64{'K': 1.0 / (1 << 20), 'M': 1.0 / 1024, 'G': 1, 'T': 1024}[v[len(v)-1]]
		if mult == 0 {
			return 0, false
		}
		n, err := strconv.ParseFloat(v[:len(v)-1], 64)
		return n * mult, err == nil
	}
	return 0, false
}

type ifaceAddr struct{ iface, addr string }

// pickIPs chooses the addresses to report, skipping loopback, link-local and container-bridge
// interfaces (Docker's docker0/br-*/veth* appear once the agent has installed Docker). IPv4 prefers a
// public address and falls back to a private one, since many Proxmox guests only have a LAN address;
// PrivateIP is the first private IPv4.
func pickIPs(addrs []ifaceAddr) (ipv4, ipv6, private string) {
	var public4 string
	for _, a := range addrs {
		if a.iface == "lo" || strings.HasPrefix(a.iface, "docker") || strings.HasPrefix(a.iface, "br-") || strings.HasPrefix(a.iface, "veth") {
			continue
		}
		s, _, _ := strings.Cut(a.addr, "/")
		ip := net.ParseIP(s)
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			continue
		}
		switch {
		case ip.To4() == nil:
			if ipv6 == "" {
				ipv6 = ip.String()
			}
		case ip.IsPrivate():
			if private == "" {
				private = ip.String()
			}
		case public4 == "":
			public4 = ip.String()
		}
	}
	ipv4 = public4
	if ipv4 == "" {
		ipv4 = private
	}
	return ipv4, ipv6, private
}

// invisibleGuest reports a 403 on the guest's own path (VM.Audit on /vms/<id>): the token cannot see
// that guest at all, as opposed to a bad token (401) or a missing node privilege.
func invisibleGuest(err error) bool {
	msg := err.Error()
	return errors.Is(err, ErrUnauthorized) && strings.Contains(msg, "Permission check failed (/vms/") && strings.Contains(msg, "VM.Audit")
}
