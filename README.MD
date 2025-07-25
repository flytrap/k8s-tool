# K8s tool

k8s install from kubeadm

## usage

unzip resource.zip to resource directory
example: ubuntu 22.04 64bit offline resource

```bash
unzip resource.zip
```

install

```bash
k8s-tools help
k8s-tools install
k8s-tools install --config config.yaml  # default config file path
k8s-tools install --config config.yaml --steps  # print install steps
k8s-tools install --config config.yaml --step 3,4  # only execute 3,4 steps, refer to above print (1 must be executed, other operations must be executed first)
```

## deploy

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/k8s-tool-linux .
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o build/k8s-tool-darwin .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/k8s-tool.exe .
```

## config file

```yaml
# config file
# config path: config/config.yaml
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
    keyPath:
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
