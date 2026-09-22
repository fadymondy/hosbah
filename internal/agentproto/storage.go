package agentproto

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// A site's storage: persistent Docker volumes mounted into a release's containers, and S3 buckets
// on a MinIO data service with a key that reaches one bucket only. Typed like everything else: the
// volume name is DERIVED from the app id and a validated slug, the mount path is validated on both
// sides of the wire, and a key's secret travels in a box sealed to the agent — never in the params.
const (
	KindVolumeUsage  = "volume.usage"  // read-only: which of an app's volumes exist and what Docker says they hold
	KindVolumeRemove = "volume.remove" // docker volume rm of one derived name; fails cleanly while a container uses it
	KindBucketCreate = "bucket.create" // mc inside the MinIO container: bucket + user + a policy limited to that bucket
	KindBucketRemove = "bucket.remove" // revoke the key (user + policy); optionally remove the bucket (empty, or forced)
)

// Limits.
const (
	MaxAppVolumes    = 10
	MaxMountPath     = 200
	VolumeNamePrefix = NamePrefix + "vol-" // cloudy-vol- before the rename; still accepted
)

var (
	volumeSlugRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	mountPathRE  = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
	bucketRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	accessKeyRE  = regexp.MustCompile(`^[A-Za-z0-9]{8,20}$`)
	containerRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// ForbiddenMountRoots: a volume is never mounted at, or under, one of these — it would hide what
// the container's runtime, its loader or its init need.
var ForbiddenMountRoots = []string{"/proc", "/sys", "/dev", "/run", "/etc", "/bin", "/sbin", "/usr", "/lib", "/lib64", "/boot", "/var/run"}

// ValidVolumeSlug: the user's name of a volume.
func ValidVolumeSlug(s string) bool { return volumeSlugRE.MatchString(s) }

// VolumeName is the Docker volume of an app's volume. Derived, never user-supplied.
func VolumeName(appID, slug string) string { return VolumeNamePrefix + appID + "-" + slug }

// ValidVolumeName reports whether name is a volume of that app.
// A volume made before the rename (cloudy-vol-<app>-<slug>) is one too.
func ValidVolumeName(appID, name string) bool {
	slug, ok := hasFamilyPrefix(name, VolumeNamePrefix+appID+"-")
	return ValidID(appID) && ok && ValidVolumeSlug(slug)
}

// CleanMountPath normalises a mount path (path.Clean) and applies the rules: absolute, the charset
// [A-Za-z0-9._/-], at most MaxMountPath characters, not "/", and not at or under a forbidden root.
// "/data/../etc" is "/etc" — refused.
func CleanMountPath(raw string) (string, error) {
	if raw == "" || len(raw) > MaxMountPath || !mountPathRE.MatchString(raw) {
		return "", fmt.Errorf("a mount path is absolute, at most %d characters of letters, digits, '.', '_', '-' and '/'", MaxMountPath)
	}
	p := path.Clean(raw)
	if p == "/" {
		return "", errors.New("a volume cannot be mounted at /")
	}
	for _, root := range ForbiddenMountRoots {
		if PathWithin(p, root) {
			return "", fmt.Errorf("a volume cannot be mounted at or under %s", root)
		}
	}
	return p, nil
}

// PathWithin reports whether the cleaned path p is parent or is below it.
func PathWithin(p, parent string) bool { return p == parent || strings.HasPrefix(p, parent+"/") }

// Mount is one volume of a release's containers.
type Mount struct {
	Volume    string `json:"volume"`
	MountPath string `json:"mount_path"`
}

// ValidateMounts checks the mounts of one app on both sides of the wire: derived volume names of
// that app, clean allowed paths, no volume or path twice, no mount inside another.
func ValidateMounts(appID string, mounts []Mount) error {
	if len(mounts) > MaxAppVolumes {
		return fmt.Errorf("an app mounts at most %d volumes", MaxAppVolumes)
	}
	for i, m := range mounts {
		if !ValidVolumeName(appID, m.Volume) {
			return fmt.Errorf("volume %q is not one of this app's", m.Volume)
		}
		clean, err := CleanMountPath(m.MountPath)
		if err != nil {
			return err
		}
		if clean != m.MountPath {
			return fmt.Errorf("mount path %q is not normalised", m.MountPath)
		}
		for _, o := range mounts[:i] {
			if o.Volume == m.Volume {
				return fmt.Errorf("volume %q is mounted twice", m.Volume)
			}
			if PathWithin(m.MountPath, o.MountPath) || PathWithin(o.MountPath, m.MountPath) {
				return fmt.Errorf("mount paths %s and %s overlap", o.MountPath, m.MountPath)
			}
		}
	}
	return nil
}

// VolumeUsageParams: volume.usage.
type VolumeUsageParams struct {
	AppID   string   `json:"app_id"`
	Volumes []string `json:"volumes"`
}

func (p VolumeUsageParams) Validate() error {
	if !ValidID(p.AppID) || len(p.Volumes) == 0 || len(p.Volumes) > MaxAppVolumes {
		return errors.New("bad volume.usage params")
	}
	for _, v := range p.Volumes {
		if !ValidVolumeName(p.AppID, v) {
			return fmt.Errorf("volume %q is not one of this app's", v)
		}
	}
	return nil
}

// VolumeUsage is one volume as the server sees it. Bytes is nil when Docker does not report a size
// (and approximate when it does: `docker system df` prints sizes rounded to four digits).
type VolumeUsage struct {
	Volume string `json:"volume"`
	Exists bool   `json:"exists"`
	Bytes  *int64 `json:"bytes"`
}

type VolumeUsageResult struct {
	Volumes []VolumeUsage `json:"volumes"`
}

// VolumeRemoveParams: volume.remove. It shares the app's lane, so it never interleaves with a deploy.
type VolumeRemoveParams struct {
	AppID  string `json:"app_id"`
	Volume string `json:"volume"`
}

func (p VolumeRemoveParams) Validate() error {
	if !ValidVolumeName(p.AppID, p.Volume) {
		return errors.New("bad volume.remove params")
	}
	return nil
}

type VolumeRemoveResult struct {
	Volume  string `json:"volume"`
	Removed bool   `json:"removed"` // false: it was already gone
}

// ValidBucket: an S3 bucket name as Hosbah allows it — 3 to 63 of a-z, 0-9 and '-', starting and
// ending alphanumeric, no dots (they break TLS on virtual-host addressing), not an "xn--" name and
// not an "-s3alias"/"--ol-s3" reserved suffix.
func ValidBucket(s string) bool {
	return bucketRE.MatchString(s) && !strings.HasPrefix(s, "xn--") && !strings.HasPrefix(s, "sthree-") &&
		!strings.HasSuffix(s, "-s3alias") && !strings.HasSuffix(s, "--ol-s3")
}

// ValidAccessKey: a generated MinIO access key (MinIO allows 3 to 20 characters).
func ValidAccessKey(s string) bool { return accessKeyRE.MatchString(s) }

// BucketPolicyName is the MinIO policy of one access key. Derived.
// Keys made before the rename have cloudy-bucket-<key>; both are accepted (ValidBucketPolicy).
func BucketPolicyName(accessKey string) string { return NamePrefix + "bucket-" + accessKey }

// ValidBucketPolicy reports whether policy is the key's policy, new or old.
func ValidBucketPolicy(accessKey, policy string) bool {
	return sameDerived(policy, BucketPolicyName(accessKey))
}

// BucketSecretPurpose is the DBSecretContext purpose of a bucket key's secret box: {"secret": …}.
func BucketSecretPurpose(bucket string) string { return "bucket:" + bucket }

// BucketCreateParams: bucket.create. RootSecret is the service's root box (purpose "root"); Secret is
// the new key's secret, a box for DBSecretContext(ServiceID, BucketSecretPurpose(Bucket)). The
// plaintext of neither is ever in the params, and both reach mc through the environment only.
type BucketCreateParams struct {
	ServiceID  string `json:"service_id"`
	Container  string `json:"container"`
	Bucket     string `json:"bucket"`
	AccessKey  string `json:"access_key"`
	Policy     string `json:"policy"`
	Secret     string `json:"secret"`
	RootSecret string `json:"root_secret"`
}

func (p BucketCreateParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case !containerRE.MatchString(p.Container):
		return errors.New("bad container name")
	case !ValidBucket(p.Bucket):
		return fmt.Errorf("bad bucket name %q", p.Bucket)
	case !ValidAccessKey(p.AccessKey) || !ValidBucketPolicy(p.AccessKey, p.Policy):
		return errors.New("bad access key or policy name")
	case p.Secret == "" || p.RootSecret == "":
		return errors.New("bucket.create needs the root and key secrets")
	}
	return nil
}

type BucketCreateResult struct {
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
}

// BucketRemoveParams: bucket.remove. The key is always revoked. DeleteData also removes the bucket:
// only if it is empty, unless Force. A non-empty bucket without Force fails BEFORE anything changes.
type BucketRemoveParams struct {
	ServiceID  string `json:"service_id"`
	Container  string `json:"container"`
	Bucket     string `json:"bucket"`
	AccessKey  string `json:"access_key"`
	Policy     string `json:"policy"`
	DeleteData bool   `json:"delete_data,omitempty"`
	Force      bool   `json:"force,omitempty"`
	RootSecret string `json:"root_secret"`
}

func (p BucketRemoveParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case !containerRE.MatchString(p.Container):
		return errors.New("bad container name")
	case !ValidBucket(p.Bucket):
		return fmt.Errorf("bad bucket name %q", p.Bucket)
	case !ValidAccessKey(p.AccessKey) || p.Policy != BucketPolicyName(p.AccessKey):
		return errors.New("bad access key or policy name")
	case p.Force && !p.DeleteData:
		return errors.New("force applies to delete_data only")
	case p.RootSecret == "":
		return errors.New("bucket.remove needs the root secret")
	}
	return nil
}

type BucketRemoveResult struct {
	Bucket        string `json:"bucket"`
	KeyRevoked    bool   `json:"key_revoked"`
	BucketRemoved bool   `json:"bucket_removed"`
}

// ErrBucketNotEmpty is the text bucket.remove fails with (the control plane turns it into a 409).
const ErrBucketNotEmpty = "the bucket is not empty"

// BucketPolicy is the MinIO policy document of a key: that one bucket and its objects, nothing else.
func BucketPolicy(bucket string) string {
	return `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Allow","Action":["s3:GetBucketLocation","s3:ListBucket","s3:ListBucketMultipartUploads"],"Resource":["arn:aws:s3:::` + bucket + `"]},` +
		`{"Effect":"Allow","Action":["s3:GetObject","s3:PutObject","s3:DeleteObject","s3:AbortMultipartUpload","s3:ListMultipartUploadParts"],"Resource":["arn:aws:s3:::` + bucket + `/*"]}]}`
}
