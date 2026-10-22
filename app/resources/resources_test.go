package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const testKubernetesVersion = "v1.37.1"

func bash(t *testing.T, script string, env ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestEmbeddedShellSyntaxAndNoLegacyOfflineInstallers(t *testing.T) {
	count := 0
	err := fs.WalkDir(Files(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(name, ".yaml") {
			contents, err := fs.ReadFile(Files(), name)
			if err != nil {
				return err
			}
			decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
			for {
				var manifest any
				err := decoder.Decode(&manifest)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Errorf("invalid YAML %s: %v", name, err)
					break
				}
			}
			return nil
		}
		if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".sha256") {
			return nil
		}
		if !strings.HasSuffix(name, ".sh") {
			t.Errorf("unexpected embedded asset: %s", name)
			return nil
		}
		count++
		data, err := fs.ReadFile(Files(), name)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "bash", "-n")
		cmd.Stdin = strings.NewReader(string(data))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v: %s", name, err, out)
		}
		for _, forbidden := range []string{"dpkg --force", "docker load", "docker install", "deb/*.deb", "curl |", "wget |"} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("%s contains %q", name, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count < 17 {
		t.Fatalf("incomplete embedded tree: %d files", count)
	}
}

func TestBundledIstioArchivesMatchChecksums(t *testing.T) {
	for _, arch := range []string{"x86_64", "aarch64"} {
		name := "istio-1.31.2-linux-"
		if arch == "x86_64" {
			name += "amd64"
		} else {
			name += "arm64"
		}
		name += ".tar.gz"
		archive, err := fs.ReadFile(Files(), "istio/"+arch+"/"+name)
		if err != nil {
			t.Fatalf("read %s archive: %v", arch, err)
		}
		checksum, err := fs.ReadFile(Files(), "istio/"+arch+"/"+name+".sha256")
		if err != nil {
			t.Fatalf("read %s checksum: %v", arch, err)
		}
		fields := strings.Fields(string(checksum))
		digest := sha256.Sum256(archive)
		if len(fields) != 2 || fields[0] != hex.EncodeToString(digest[:]) || fields[1] != name {
			t.Fatalf("bundled %s Istio archive checksum mismatch", arch)
		}
	}
	script, err := fs.ReadFile(Files(), "istio/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "github.com/istio/istio/releases/download") || !strings.Contains(string(script), `verify_sha256 "$archive_path" "$archive_path.sha256"`) {
		t.Fatal("Istio installer must verify and use the bundled release archive")
	}
}

func TestAppInstallUsesBundledOfflineAssets(t *testing.T) {
	install, err := fs.ReadFile(Files(), "app/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"metrics-server.yaml", "charts/kube-state-metrics-$2.tgz", `verify_sha256 "$chart" "$chart.sha256"`} {
		if !strings.Contains(string(install), required) {
			t.Errorf("app installer does not use bundled asset %q", required)
		}
	}
	for _, forbidden := range []string{"helm repo add", "helm repo update", "prometheus-community.github.io", "releases/download/"} {
		if strings.Contains(string(install), forbidden) {
			t.Errorf("app installer still uses online source %q", forbidden)
		}
	}
	manifest, err := fs.ReadFile(Files(), "app/metrics-server.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "registry.k8s.io/metrics-server/metrics-server:"+MetricsServerVersion) {
		t.Errorf("bundled Metrics Server manifest does not match pinned version %s", MetricsServerVersion)
	}
	if _, err := fs.Stat(Files(), "app/charts/kube-state-metrics-"+KubeStateMetricsChartVersion+".tgz"); err != nil {
		t.Fatalf("missing bundled kube-state-metrics chart: %v", err)
	}
	if _, err := fs.Stat(Files(), "app/charts/kube-state-metrics-"+KubeStateMetricsChartVersion+".tgz.sha256"); err != nil {
		t.Fatalf("missing kube-state-metrics chart checksum: %v", err)
	}
}

func TestCalicoManifestIsBundledAndMatchesPinnedVersion(t *testing.T) {
	manifest, err := fs.ReadFile(Files(), "calico/calico.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "image: quay.io/calico/node:"+CalicoVersion) {
		t.Fatalf("bundled Calico manifest does not use pinned version %s", CalicoVersion)
	}
	script, err := fs.ReadFile(Files(), "calico/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "raw.githubusercontent.com") || !strings.Contains(string(script), `cp "$manifest" "$WORK/calico.yaml"`) {
		t.Fatal("Calico installation must apply its bundled manifest without downloading it")
	}
}

func TestChecksumRejectsCorruptionAndMissingEntry(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "artifact")
	if err := os.WriteFile(artifact, []byte("verified artifact\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A mock curl copies fixtures, so no network access is possible.
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	mock := "#!/usr/bin/env bash\nset -eu\nwhile [[ $# -gt 0 ]]; do if [[ $1 == -o ]]; then cp \"$FIXTURE\" \"$2\"; exit; fi; shift; done\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "ARTIFACT=" + artifact, "FIXTURE=" + artifact}
	script := `source files/common.sh
(cd "$(dirname "$ARTIFACT")"; sha256sum artifact) > "$ARTIFACT.sha256"
verify_sha256 "$ARTIFACT" "$ARTIFACT.sha256"
download https://example.invalid/artifact "$ARTIFACT.copy"
cmp "$ARTIFACT" "$ARTIFACT.copy"`
	if out, err := bash(t, script, env...); err != nil {
		t.Fatalf("valid checksum: %v %s", err, out)
	}
	if err := os.WriteFile(artifact, []byte("corruption"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := bash(t, `source files/common.sh; verify_sha256 "$ARTIFACT" "$ARTIFACT.sha256"`, env...); err == nil || !strings.Contains(out, "Checksum mismatch") {
		t.Fatalf("corrupt checksum accepted: %v %s", err, out)
	}
	if err := os.WriteFile(artifact+".sha256", []byte(strings.Repeat("0", 64)+"  another-file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := bash(t, `source files/common.sh; verify_sha256 "$ARTIFACT" "$ARTIFACT.sha256"`, env...); err == nil || !strings.Contains(out, "No SHA256") {
		t.Fatalf("missing checksum accepted: %v %s", err, out)
	}
	if _, err := bash(t, `source files/common.sh; download http://example.invalid/artifact "$ARTIFACT.copy"`, env...); err == nil {
		t.Fatal("HTTP artifact accepted")
	}
}

func TestImageListUsesConfiguredVersionAndRepository(t *testing.T) {
	dir := t.TempDir()
	kubeadm := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" > \"$KUBEADM_LOG\"\nprintf '%s\\n' 'registry.k8s.io/kube-apiserver:v1.37.1' 'registry.k8s.io/pause:3.10'\n"
	ctr := "#!/usr/bin/env bash\nif [[ $1 == -n && $2 == k8s.io && $3 == images && $4 == list && $5 == -q ]]; then exit 0; fi\nif [[ $1 == -n && $2 == k8s.io && $3 == images && $4 == pull ]]; then printf 'pulled:%s\\n' \"$5\"; exit 0; fi\nexit 2\n"
	for name, contents := range map[string]string{"kubeadm": kubeadm, "ctr": ctr} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Intercept only escalation; execute the actual embedded image script and helper.
	script := `bash -c 'source() { builtin source "$@"; require_root() { :; }; }; source "$0" "$@"' files/kubeadm/images/install.sh v1.37.1 unix:///run/containerd/containerd.sock registry.k8s.io`
	kubeadmLog := filepath.Join(dir, "kubeadm.log")
	out, err := bash(t, script, "PATH="+dir+":"+os.Getenv("PATH"), "KUBEADM_LOG="+kubeadmLog)
	if err != nil {
		t.Fatalf("image script: %v %s", err, out)
	}
	arguments, err := os.ReadFile(kubeadmLog)
	if err != nil || strings.TrimSpace(string(arguments)) != "config images list --kubernetes-version v1.37.1 --image-repository registry.k8s.io" {
		t.Fatalf("unexpected kubeadm image listing arguments: %q %v", arguments, err)
	}
	want := "Pulling missing image: registry.k8s.io/kube-apiserver:v1.37.1\npulled:registry.k8s.io/kube-apiserver:v1.37.1\nPulling missing image: registry.k8s.io/pause:3.10\npulled:registry.k8s.io/pause:3.10\n"
	if out != want {
		t.Fatalf("unexpected missing image pulls: %q", out)
	}
}

func TestImagePullSkipsExistingContainerdImages(t *testing.T) {
	dir := t.TempDir()
	imagesFile := filepath.Join(dir, "images")
	pullLog := filepath.Join(dir, "pulls")
	if err := os.WriteFile(imagesFile, []byte("registry.k8s.io/kube-apiserver:v1.37.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	kubeadm := "#!/usr/bin/env bash\nprintf '%s\\n' 'registry.k8s.io/kube-apiserver:v1.37.1' 'registry.k8s.io/kube-controller-manager:v1.37.1' 'registry.k8s.io/pause:3.10'\n"
	ctr := "#!/usr/bin/env bash\nif [[ $1 == -n && $2 == k8s.io && $3 == images && $4 == list && $5 == -q ]]; then cat \"$CTR_IMAGES\"; exit; fi\nif [[ $1 == -n && $2 == k8s.io && $3 == images && $4 == pull ]]; then printf '%s\\n' \"$5\" >> \"$PULL_LOG\"; printf '%s\\n' \"$5\" >> \"$CTR_IMAGES\"; exit; fi\nprintf 'unexpected ctr arguments: %s\\n' \"$*\" >&2; exit 2\n"
	for name, contents := range map[string]string{"kubeadm": kubeadm, "ctr": ctr} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0700); err != nil {
			t.Fatal(err)
		}
	}
	script := `source() { builtin source "$@"; require_root() { :; }; }; source files/kubeadm/images/install.sh v1.37.1 unix:///run/containerd/containerd.sock registry.k8s.io`
	out, err := bash(t, script, "PATH="+dir+":"+os.Getenv("PATH"), "CTR_IMAGES="+imagesFile, "PULL_LOG="+pullLog)
	if err != nil {
		t.Fatalf("image pull script failed: %v %s", err, out)
	}
	if !strings.Contains(out, "Image already exists locally, skipping pull: registry.k8s.io/kube-apiserver:v1.37.1") {
		t.Fatalf("existing image was not skipped: %s", out)
	}
	pulled, err := os.ReadFile(pullLog)
	if err != nil {
		t.Fatalf("read image pull log: %v", err)
	}
	want := "registry.k8s.io/kube-controller-manager:v1.37.1\nregistry.k8s.io/pause:3.10\n"
	if string(pulled) != want {
		t.Fatalf("pulled incorrect images: %q", pulled)
	}
}

func TestExactVersionsAndInputValidation(t *testing.T) {
	for _, version := range []string{"latest", "1.37", "1.37.1; touch /tmp/bad", ""} {
		if _, err := bash(t, `source files/common.sh; validate_version "$VERSION"`, "VERSION="+version); err == nil {
			t.Errorf("accepted version %q", version)
		}
	}
	for _, ip := range []string{"999.1.1.1", "127.0.0.1\n}", "::1", "1.2.3"} {
		if _, err := bash(t, `source files/common.sh; validate_ip "$ADDRESS"`, "ADDRESS="+ip); err == nil {
			t.Errorf("accepted IP %q", ip)
		}
	}
	if _, err := bash(t, `source files/common.sh; validate_version v1.37.1; validate_ip 10.0.0.1`); err != nil {
		t.Fatal(err)
	}
}

func TestExactAptVersionLookupConsumesCompleteOutput(t *testing.T) {
	dir := t.TempDir()
	mock := "#!/usr/bin/env bash\nexec yes \"$1 | 1.37.1-1.1 | https://packages.invalid/$1\" | head -n 50000\n"
	if err := os.WriteFile(filepath.Join(dir, "apt-cache"), []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := bash(t, `source files/common.sh; exact_apt_package_version kubelet 1.37.1`, "PATH="+dir+":"+os.Getenv("PATH"))
	if err != nil {
		t.Fatalf("exact apt version lookup failed: %v %s", err, out)
	}
	if strings.TrimSpace(out) != "1.37.1-1.1" {
		t.Fatalf("unexpected selected package version: %q", out)
	}
}

func TestGeneratedKubeadmConfigurationUsesPinnedVersionAndContainerd(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "kubeadm"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$COMMAND_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("files/kubeadm/start.sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", `source() { builtin source "$@"; require_root() { :; }; }; source "$0" "$@"`, script, "192.0.2.100", "master", "192.0.2.1", DefaultCRISocket, testKubernetesVersion, "registry.k8s.io")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "COMMAND_LOG="+filepath.Join(dir, "arguments"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate kubeadm config: %v %s", err, out)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "kubeadm-config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
	manifests := map[string]map[string]any{}
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		manifests[doc["kind"].(string)] = doc
	}
	init := manifests["InitConfiguration"]
	cluster := manifests["ClusterConfiguration"]
	if init["apiVersion"] != "kubeadm.k8s.io/v1beta4" || init["nodeRegistration"].(map[string]any)["criSocket"] != DefaultCRISocket || cluster["kubernetesVersion"] != testKubernetesVersion || cluster["controlPlaneEndpoint"] != "192.0.2.100:16443" || manifests["KubeletConfiguration"]["cgroupDriver"] != "systemd" {
		t.Fatalf("incorrect generated configuration: %s", contents)
	}
	if strings.Contains(string(contents), "bootstrapTokens") {
		t.Fatal("permanent bootstrap token configured")
	}
	args, err := os.ReadFile(filepath.Join(dir, "arguments"))
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "init\n--config\nkubeadm-config.yaml\n--upload-certs\n" {
		t.Fatalf("unexpected kubeadm command: %s", args)
	}
}

func TestLokiStorageSelectionAndManifest(t *testing.T) {
	for _, tc := range []struct{ name, existing, defaultClass, want string }{
		{"fresh no storage", "", "", "ephemeral"},
		{"fresh default class", "", "nfs-storage", "persistent"},
		{"preserve existing PVC", "loki:storage", "", "persistent"},
		{"preserve existing emptyDir", "loki:", "nfs-storage", "ephemeral"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mock := "#!/bin/sh\ncase \"$*\" in *'get statefulset'*) printf '%s' \"$EXISTING\";; *'get storageclass'*) printf '%s' \"$DEFAULT_CLASS\";; *) exit 1;; esac\n"
			if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(mock), 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "loki.yaml")
			out, err := bash(t, `source files/common.sh; source files/istio/loki-storage.sh; mode=$(loki_storage_mode); render_loki_manifest "$mode" files/istio/conf/loki/loki.yaml "$TARGET"; printf '%s' "$mode"`, "PATH="+dir+":"+os.Getenv("PATH"), "EXISTING="+tc.existing, "DEFAULT_CLASS="+tc.defaultClass, "TARGET="+target)
			if err != nil || out != tc.want {
				t.Fatalf("storage selection: %q %v", out, err)
			}
			contents, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
			for {
				var doc map[string]any
				err := decoder.Decode(&doc)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if doc["kind"] != "StatefulSet" {
					continue
				}
				spec := doc["spec"].(map[string]any)
				_, hasClaims := spec["volumeClaimTemplates"]
				if hasClaims != (tc.want == "persistent") {
					t.Fatal("incorrect PVC template")
				}
				if tc.want == "ephemeral" {
					volumes := spec["template"].(map[string]any)["spec"].(map[string]any)["volumes"].([]any)
					found := false
					for _, value := range volumes {
						v := value.(map[string]any)
						if v["name"] == "storage" {
							_, found = v["emptyDir"]
						}
					}
					if !found {
						t.Fatal("no ephemeral storage volume")
					}
				}
			}
		})
	}
}
