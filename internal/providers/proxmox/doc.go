// Package proxmox is the Proxmox VE driver, registered with package providers as "proxmox".
//
// It creates QEMU VMs by full-cloning a cloud-init template, and unprivileged LXC containers from a
// vztmpl archive. Provider IDs are "<node>/qemu/<vmid>" and "<node>/lxc/<vmid>".
//
// # Credential
//
// Config keys: api_url (https://pve:8006), token_id (USER@REALM!TOKENID, e.g. cloudy@pve!ctl),
// node, storage (guest disks), bridge (default vmbr0), pool (optional), template_vmid (the VM
// cloud-init template), lxc_template (a volid such as
// local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst), snippets_storage + snippets_dir
// (optional, see "User-data" below), tls_fingerprint (SHA-256 of the server's leaf certificate, hex,
// colons optional) and insecure_tls. Secret is the token secret.
//
// Proxmox serves a self-signed certificate, so set tls_fingerprint: the driver then accepts exactly
// that leaf certificate and nothing else. insecure_tls=true (only honoured without a fingerprint)
// disables verification entirely and exists for testing. With neither, the system roots are used,
// which suits a Proxmox fronted by a publicly trusted certificate.
//
// Every request carries "Authorization: PVEAPIToken=<token_id>=<secret>". The secret is never logged
// and never appears in an error.
//
// # Endpoints called (all under /api2/json)
//
//	GET    /version                                             Validate
//	GET    /nodes                                               Regions
//	GET    /nodes/{node}/status                                 Validate
//	GET    /nodes/{node}/qemu                                   Images (templates: template == 1)
//	GET    /nodes/{node}/storage/{storage}/content?content=vztmpl  Images (LXC templates)
//	GET    /cluster/nextid                                      Create
//	POST   /nodes/{node}/qemu/{template_vmid}/clone             Create vm (full clone)
//	GET    /nodes/{node}/qemu/{vmid}/config                     Create vm (current scsi0 size)
//	POST   /nodes/{node}/qemu/{vmid}/config                     Create vm (cores, memory, cloud-init)
//	PUT    /nodes/{node}/qemu/{vmid}/resize                     Create vm (grow scsi0)
//	POST   /nodes/{node}/lxc                                    Create lxc
//	GET    /nodes/{node}/{qemu|lxc}/{vmid}/status/current       Get, Delete
//	POST   /nodes/{node}/{qemu|lxc}/{vmid}/status/{start|stop|shutdown|reboot}  Create, Power, Delete
//	GET    /nodes/{node}/lxc/{vmid}/interfaces                  Get (lxc IPs)
//	GET    /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces  Get (vm IPs, needs the guest agent)
//	DELETE /nodes/{node}/{qemu|lxc}/{vmid}?purge=1&destroy-unreferenced-disks=1  Delete
//	GET    /nodes/{node}/tasks/{upid}/status                    waiting on every async call
//
// # Token and permissions
//
// Use a dedicated user and a privilege-separated token, cloudy@pve!ctl — never root@pam. With
// privilege separation a token's rights are the intersection of the user's and the token's ACLs,
// so grant each entry to both:
//
//   - PVEVMAdmin on /pool/cloudy (propagated). Guests are created into the pool; the VM template must
//     be a member of the pool too so the token can see and clone it.
//   - Datastore.AllocateSpace on /storage/<storage> (clone targets, rootfs, resize).
//   - Datastore.Audit on the storage holding the LXC templates and on snippets_storage (referencing
//     a vztmpl or snippet volume, and listing templates in Images).
//   - SDN.Use on /sdn/zones/localnetwork/<bridge> (PVE 8 checks bridge access on net0).
//   - Sys.Audit on /nodes/<node> (PVEAuditor there), used only by Validate's node-status probe.
//
// Datastore.AllocateTemplate is not needed: the driver never uploads or downloads templates.
// Hookscripts require root@pam to set, so the driver never uses them.
//
// # User-data
//
// The PVE API cannot upload cloud-init snippets: the storage upload endpoint accepts only iso,
// vztmpl and import content. A VM's custom user-data must therefore be written as a file into a
// "snippets" storage by some other route, which is what SnippetStore abstracts. DirSnippetStore
// writes into snippets_dir, a directory the control plane has mounted that backs snippets_storage
// (an NFS/CIFS/dir storage with the snippets content type). With a store, Create writes
// cloudy-<vmid>.yaml and sets cicustom=user=<snippets_storage>:snippets/cloudy-<vmid>.yaml. Note that
// a custom user= snippet replaces Proxmox's generated user-data, so it must create the user and keys
// itself (providers.AgentUserData does).
//
// Without a store, DeliversUserData("vm") is false: Create still sets ciuser=cloudy and sshkeys and
// the control plane bootstraps the agent over SSH (package sshboot). LXC has no cloud-init, so
// DeliversUserData("lxc") is always false; its keys go to root through ssh-public-keys.
//
// LXC containers are unprivileged with features nesting=1,keyctl=1 for Docker. Some PVE versions
// let only root@pam change feature flags other than nesting; if creation is refused for that
// reason, pass Options["features"] = "nesting=1".
package proxmox
