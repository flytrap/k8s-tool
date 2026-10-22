#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
platform
for version in "$@"; do validate_version "$version"; done
[[ $# == 3 ]] || fail 'Usage: install.sh containerdVersion runcVersion cniVersion'
[[ ! -e /etc/kubernetes/kubelet.conf ]] || fail 'Existing Kubernetes node; runtime migration requires a separate procedure'
[[ ! -S /var/run/docker.sock && ! -S /var/run/crio/crio.sock ]] || fail 'Existing incompatible runtime'
workdir
cd "$WORK"
archive="containerd-$1-linux-$ARCH.tar.gz"
url="https://github.com/containerd/containerd/releases/download/v$1/$archive"
checked_download "$url" "$url.sha256sum" "$archive"
tar -xzf "$archive" -C /usr/local
url="https://github.com/opencontainers/runc/releases/download/v$2/runc.$ARCH"
checked_download "$url" "https://github.com/opencontainers/runc/releases/download/v$2/runc.sha256sum" "runc.$ARCH"
install -m 0755 "runc.$ARCH" /usr/local/sbin/runc
archive="cni-plugins-linux-$ARCH-v$3.tgz"
url="https://github.com/containernetworking/plugins/releases/download/v$3/$archive"
checked_download "$url" "$url.sha256" "$archive"
mkdir -p /opt/cni/bin /etc/containerd
tar -xzf "$archive" -C /opt/cni/bin
/usr/local/bin/containerd config default > "$WORK/config.toml"
# Containerd 2 uses io.containerd.cri.v1.runtime and runc.options.
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' "$WORK/config.toml"
grep -q 'SystemdCgroup = true' "$WORK/config.toml" || fail 'Cannot enable systemd cgroups'
install -m 0644 "$WORK/config.toml" /etc/containerd/config.toml
cat > /etc/systemd/system/containerd.service <<'EOF'
[Unit]
Description=containerd container runtime
After=network.target local-fs.target
[Service]
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=/usr/local/bin/containerd
Type=notify
Delegate=yes
KillMode=process
Restart=always
RestartSec=5
LimitNOFILE=infinity
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
OOMScoreAdjust=-999
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable containerd
systemctl restart containerd
