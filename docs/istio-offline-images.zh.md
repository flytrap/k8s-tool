# Istio 1.31.2 离线镜像

当前安装流程使用 Istio 官方 `default` profile，安装 `prometheus`、`grafana`、`kiali`，并额外部署 Loki 和 OpenTelemetry Collector。对应镜像清单维护在 [`scripts/istio-images.sh`](../scripts/istio-images.sh)。清单包含 Istio 1.31.2 控制面与代理、该版本发布包自带的三个 addon 镜像，以及项目 Loki / OpenTelemetry 清单中的镜像。

## 在联网机器下载并打包

联网机器需安装 containerd 2.x 的 `ctr`，并能访问 Docker Hub、Quay 和 GHCR。脚本会按目标架构拉取并分别导出 amd64 与 arm64 包：

```bash
bash scripts/istio-images.sh list
bash scripts/istio-images.sh export amd64
bash scripts/istio-images.sh export arm64
```

拉取和导出均按目标架构逐个处理，不要用 `ctr images pull --all-platforms`。containerd 2.3.6 的该选项会尝试解包所有架构及索引中的 `unknown/unknown` 条目，可能以 `mismatched image rootfs and manifest layers` 失败；containerd 上游也记录了同类问题。[上游问题](https://github.com/containerd/containerd/issues/10196)

按目标节点架构传输对应的 `istio-images-1.31.2-linux-*.tar` 文件。若集群是混合架构，两个包都要传到所有节点。

## 在每台 Kubernetes 节点导入

```bash
sudo ctr --namespace k8s.io images import istio-images-1.31.2-linux-amd64.tar
sudo ctr --namespace k8s.io images list
```

arm64 节点改为导入 `istio-images-1.31.2-linux-arm64.tar`。镜像必须导入每一台可能运行 Istio、Prometheus、Grafana、Kiali、Loki 或 OpenTelemetry Pod 的节点，并进入 `k8s.io` namespace。导入完成后再运行项目的 Istio 安装步骤。

Istio 1.31.2 Linux amd64 与 arm64 release archive（含 `istioctl` 和 addon YAML）也已随项目按架构内嵌，并在安装时校验 SHA256；目标节点不需要再从 GitHub 下载该安装包。升级 Istio、addon 或 Loki / OpenTelemetry 版本时，须同步更新内嵌 archive、校验和及镜像清单。
