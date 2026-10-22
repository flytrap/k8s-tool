# K8s tool

通过 SSH 与 kubeadm 在线安装 Kubernetes 集群，使用 containerd 运行时。

## 组件与版本

| 组件 | 版本 |
| --- | --- |
| Kubernetes (kubeadm / kubelet / kubectl) | `v1.37.1` |
| containerd / runc / CNI plugins | `2.3.6` / `1.4.2` / `1.8.0` |
| Istio CLI / control plane / proxy | `1.31.2` |
| Calico | `v3.33.0` |
| Helm | `v3.21.3` |
| Metrics Server | `v0.8.1` |
| kube-state-metrics Helm chart | `8.6.0` |
| NFS provisioner Helm chart | `4.0.18` |

Kubernetes 和 Istio 版本只由 `config.yaml` 中的 `kubernetes-version` 与
`istio-version` 决定，并传递给软件包安装、kubeadm 初始化、镜像拉取、扩容检查和
Istio 安装。版本须为稳定版 `v1.x.y` 与 `1.x.y`。其他组件版本统一定义在
[app/resources/resources.go](app/resources/resources.go)。指定版本不存在时安装会失败，
不会回退到其他版本。每份配置文件都必须显式包含这两个版本字段。
containerd 与 kubelet 均使用 systemd cgroup；默认 CRI socket 为
`unix:///run/containerd/containerd.sock`。
HAProxy、Keepalived、Chrony 和 NFS 客户端从操作系统在线软件源安装。

当前 [Istio 支持矩阵](https://istio.io/latest/docs/releases/supported-releases/)
包含 Kubernetes 1.37，[Calico 3.33 要求](https://docs.tigera.io/calico/latest/getting-started/kubernetes/requirements)
也列出了 Kubernetes 1.37。

## 在线安装要求

- 目标节点为使用 systemd 的 Ubuntu / Debian，支持 amd64 与 arm64；Calico 3.33 要求 Linux 内核 5.10 或更新版本。
- 操作系统软件源可访问；节点能够访问 `pkgs.k8s.io`、GitHub / `raw.githubusercontent.com`、`get.helm.sh` 及所需 Helm 仓库。
- Calico 3.33.0 的 YAML 清单已随项目内嵌，不会在安装时从 `raw.githubusercontent.com` 下载；节点仍需访问镜像仓库。
- Metrics Server `v0.8.1` 清单与 kube-state-metrics `8.6.0` Helm chart 已内嵌，step 14 不再访问 GitHub 或 Prometheus Helm 仓库；镜像仍需在线拉取或预先导入。
- 节点能够在线拉取 `registry.k8s.io`、`quay.io`、`docker.io` 等镜像仓库。
- SSH 用户为 root，或具备 sudo 权限；脚本会为部署用户配置免密 sudo。
- 配置 VIP、节点地址、认证信息，并提前配置可信主机密钥或显式启用跳过验证。
- 放行 Kubernetes、Calico 与 HA 通信所需网络端口；脚本不会自动关闭防火墙。

安装脚本与配置模板已内嵌在可执行文件中，不需要解压 `resource.zip`，也不依赖
本机 `resource` 目录。Istio 1.31.2 的 amd64 / arm64 安装包也已按架构内嵌并校验
SHA256；其他二进制下载校验上游 SHA256，软件包使用签名软件源。

## 使用说明

```bash
k8s-tool help
k8s-tool install --steps
k8s-tool install --config config.yaml
k8s-tool install --config config.yaml --step 4,5,6
k8s-tool install --update --steps
k8s-tool install --update --config config.yaml
```

默认读取当前目录的 `config.yaml`。安装步骤 4–6 分别为安装 containerd、安装
Kubernetes 工具、在线拉取 Kubernetes 镜像；扩容步骤 5–7 对应这三项。
与旧离线版相比，工具安装必须先于镜像拉取，请重新执行 `--steps` 核对编号。
分步执行假定此前依赖已完成；连接步骤会自动补齐，扩容还会自动补齐集群检查。
第 6 步会检查 containerd 的 `k8s.io` 命名空间，跳过已存在的精确镜像引用，只拉取缺失的
Kubernetes 镜像。预导入镜像时，名称须与 `kubeadm config images list` 输出一致。

Istio 相关镜像的离线打包与导入说明见
[Istio 离线镜像指南](docs/istio-offline-images.zh.md)。
Metrics Server 与 kube-state-metrics 镜像的离线打包与导入说明见
[应用组件离线资源说明](docs/app-offline-assets.zh.md)。

## 拓扑与扩容

控制平面必须同时拥有 `etcd` 角色；独立 etcd 拓扑不受支持。
节点 hostname 必须是唯一的小写 DNS 名称，节点地址与 VIP 使用 IPv4。
扩容配置包含全部已有控制平面和新增节点，管理入口由已注册节点自动探测。

`--update` 用于扩容 **Kubernetes v1.37.1、containerd 2.x 的已有集群**，不是
跨版本升级或 Docker 工作负载迁移命令。程序会检查 API server、kubelet 和运行时，
不兼容时在初始化新增节点之前退出。旧 v1.29.2 / Docker 集群需单独规划逐版本
升级、节点排空与运行时迁移，不会由安装脚本直接卸载 Docker 或覆盖已加入集群的节点。

扩容第 9 步加入节点，第 10 步同步 HAProxy 后端与 Keepalived 对等节点。
分步恢复使用 `--update --step 9,10`，join 后可单独重试第 10 步。
HA 同步先更新已运行实例，再启动新增实例；没有活跃实例时，先恢复已有 VIP。
`region` 留空使用局域网 VIP；填写 AWS region 后，脚本在线安装所需 AWS CLI，
使用实例角色执行 EC2 私有 IP 重新分配。

## SSH 验证与超时

默认验证当前用户 `~/.ssh/known_hosts`，也可以为节点配置
`host-key-fingerprint: SHA256:<已验证指纹>`。`ssh.known-hosts` 留空时使用默认文件。

```bash
k8s-tool install --config config.yaml --insecure-skip-host-key-check
# 显式 false 优先于配置文件，恢复本次执行的验证。
k8s-tool install --config config.yaml --insecure-skip-host-key-check=false
```

`ssh.insecure-skip-host-key-check` 默认 `false`；启用后同时跳过 known_hosts 和
SHA256 指纹验证。命令行显式设置优先于配置文件。
默认连接超时 30 秒、命令超时 15 分钟、传输超时 30 分钟，可通过 `ssh` 配置修改。
中断或超时会关闭受影响连接，但不会回滚已经启动的远程操作；重试前检查节点状态。

## 监控与存储

Istio 使用相同版本发布包中的 Prometheus、Grafana、Kiali 示例配置，保留原有
Loki 2.7.3 / OpenTelemetry Collector 0.73.0 日志链路，镜像均在线拉取；日志组件
版本未在此次变更中升级。新部署有默认 StorageClass 时 Loki 使用 PVC；没有时使用
`emptyDir`，Pod 替换会丢失日志。已存在的 Loki 保留原存储类型。Telemetry 使用 `telemetry.istio.io/v1`。
Metrics Server 保留原安装的 `--kubelet-insecure-tls` 行为；生产环境应配置可信
kubelet 服务证书并移除此参数。

NTP server 留空时跳过 Chrony。NFS server 留空时跳过 provisioner，仍安装 NFS
客户端；配置 NFS 后在线安装固定版本 Helm chart。
本地回归使用模拟命令和 SSH/SFTP 服务，不代表真实安装、网络或 VIP 故障切换已经验证。

## 编译

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/k8s-tool-linux .
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o build/k8s-tool-darwin .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/k8s-tool.exe .
```

## 默认配置

根目录 [config.yaml](config.yaml) 为默认示例，执行前替换地址和认证信息。
用户名和密码为 base64 编码；非空 `keyPath` 选择私钥认证。

```yaml
# Example deployment configuration. Replace node addresses and credentials before use.
namespace: default
registry: registry.k8s.io
# 为本次部署选择 Kubernetes 与 Istio 版本。
kubernetes-version: v1.37.1
istio-version: 1.31.2
cri-socket: unix:///run/containerd/containerd.sock

vip: 192.168.56.151
# Empty uses a LAN VIP; set an AWS region to enable EC2 private-IP reassignment.
region: ""

ssh:
  # false: verify known_hosts or the node's SHA256 fingerprint.
  # true: skip both checks. An explicit CLI value overrides this setting.
  insecure-skip-host-key-check: false
  # Empty uses the current user's ~/.ssh/known_hosts.
  known-hosts: ""
  connect-timeout: 30s
  command-timeout: 15m
  transfer-timeout: 30m

ntp:
  # Empty disables Chrony installation.
  server: ""
  allow: ""
  timezone: Asia/Shanghai

nodes:
  - address: 192.168.56.101
    hostname: master1
    role: [etcd, controlplane, worker]
    port: 22
    # Example only: both values encode "deploy" using base64.
    username: ZGVwbG95
    password: ZGVwbG95
    # A non-empty keyPath selects private-key authentication instead of password.
    keyPath: ""
    # Optional alternative to known_hosts: SHA256:<verified server fingerprint>.
    host-key-fingerprint: ""

nfs:
  # Empty disables the NFS provisioner; NFS client utilities are still installed.
  server: ""
  path: ""
```
