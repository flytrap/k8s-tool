# Metrics Server 与 kube-state-metrics 离线资源

项目已内置 Metrics Server `v0.8.1` 的组件清单，以及 kube-state-metrics Helm chart `8.6.0`（应用镜像 `v2.20.0`）。安装脚本直接使用这些本地资源，不添加或更新 Helm 仓库，也不下载组件清单。镜像仍需从可访问 `registry.k8s.io` 的机器导出并在节点导入。

## 导出镜像

在可访问 `registry.k8s.io` 的机器上，需要安装 containerd 2.x 的 `ctr`：

```bash
bash scripts/app-images.sh list
bash scripts/app-images.sh export amd64
bash scripts/app-images.sh export arm64
```

按需将 `app-images-metrics-server-v0.8.1-kube-state-metrics-v2.20.0-linux-*.tar` 传到集群节点。混合架构集群需要分别准备 amd64 和 arm64 包。

## 导入镜像

在每台可能运行这两个工作负载的节点上，导入对应架构的包：

```bash
sudo ctr --namespace k8s.io images import app-images-metrics-server-v0.8.1-kube-state-metrics-v2.20.0-linux-amd64.tar
sudo ctr --namespace k8s.io images list | grep -E 'metrics-server|kube-state-metrics'
```

arm64 节点将文件名中的 `amd64` 替换为 `arm64`。导入至 `k8s.io` namespace 后，kubelet 才能使用这些镜像。

`scripts/app-images.sh import <archive.tar>` 也可用于导入：

```bash
bash scripts/app-images.sh import app-images-metrics-server-v0.8.1-kube-state-metrics-v2.20.0-linux-amd64.tar
```
