# K8s tool

k8s 安装部署自动化处理（kubeadm）

## 使用说明

需要解压资源文件到当前路径
示例为 ubuntu 22.04 离线资源

```bash
unzip resource.zip
```

安装集群

```bash
k8s-tools help # 帮助
k8s-tools install # 安装
k8s-tools install --config config.yaml  # 默认配置文件路径
k8s-tools install --config config.yaml --steps  # 打印安装步骤
k8s-tools install --config config.yaml --step 3,4 # 只执行3,4步骤, 参考上面的打印（1一定执行，其他操作都必须先连接）
```

## 编译

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/k8s-tool-linux .
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o build/k8s-tool-darwin .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/k8s-tool.exe .
```

## 配置文件

```yaml
# 配置文件
# 配置文件路径： config/config.yaml
namespace: default
registry: registry.cn-hangzhou.aliyuncs.com

ntp:
  server: 192.168.1.101
  allow: 192.168.1.101/16
  timezone: Asia/Shanghai

vip: 192.168.56.151

nodes:
  - address: 192.168.56.101
    hostname: master1
    role: [etcd, controlplane, worker]
    port: 22
    username: ZGVwbG95
    password: ZGVwbG95
  # - address: 192.168.56.102
  #   hostname: master2
  #   role: [etcd, controlplane, worker]
  #   port: 22
  #   username: ZGVwbG95
  #   password: ZGVwbG95

nfs:
  server: 192.168.57.101
  path: /data/nfs
```
