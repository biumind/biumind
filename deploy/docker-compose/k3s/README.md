# k3s kubeconfig

本目录用于本地 `docker-compose --profile k8s` 起的 k3s 单节点集群。

## kubeconfig 哪来的 / Where kubeconfig comes from

- `docker-compose.yml` 的 `k3s` 服务启动时自动把 kubeconfig 写到 **仓库根的 `./k3s/kubeconfig.yaml`**（已被根 `.gitignore` 的 `/k3s/` 规则忽略，**不会进 git**）。
- Sandbox 服务的 K8s 驱动通过 `KUBECONFIG` 指过去。

## 真集群 / Pointing at a real cluster

如果你要把 Sandbox 指向一个**真的**远端 K8s 集群：

```bash
export KUBECONFIG=/path/to/your/real-kubeconfig.yaml
export SANDBOX_K8S_IMAGE=<your-pause-image>
# 可选：SANDBOX_K8S_RUNTIMECLASS=gvisor
```

**任何含真实凭据的 kubeconfig 都不要提交到 git**——`client-key-data` / `client-certificate-data` / CA 一旦公开即等同交出集群凭据。根 `.gitignore` 已用 `**/kubeconfig*.yaml` 拦截；本目录下只放本说明。
