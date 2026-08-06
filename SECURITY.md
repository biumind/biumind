# Security Policy

## 报告漏洞 / Reporting a Vulnerability

BiuMind 认真对待安全问题。**请勿通过公开 GitHub issue 报告安全漏洞。**

请通过以下任一方式私下上报：

- **GitHub Security Advisories**：仓库 → Security → Report a vulnerability（首选 / preferred）
- 邮箱：**geekidentity@163.com**

上报时请尽量包含：

- 问题类型（如 SQL 注入、XSS、权限绕过、密钥泄漏）
- 受影响版本 / commit
- 复现步骤（最小可复现示例）
- 影响评估与建议修复（如有）

我们会在 **3 个工作日内**确认收到，并在 **14 天内**给出初步评估与修复计划。修复并发布后，我们会在致谢中感谢你（除非你希望匿名）。

Please **do not** open a public GitHub issue for security vulnerabilities. Report privately via GitHub Security Advisories (preferred) or geekidentity@163.com. We acknowledge within 3 business days and aim for an initial assessment within 14 days.

## 支持版本 / Supported Versions

| Version | Supported          |
|---------|--------------------|
| latest `main` / latest release | ✅ |
| older releases                   | ❌ |

## 自托管安全注意 / Self-hosting notes

- **轮换默认密钥**：本地起服务前，把 `deploy/docker-compose/.env.example` 里的占位符（`*_change_me`、空 key 字段）全部换成强随机值。**永远不要在生产沿用示例值**。
- **BYOK / 凭据存储**：用户 API key 经 keychain / master key 加密落盘，`BYOK_MASTER_KEY` 与 `BIUMIND_MASTER_KEY` 必须在部署时设为强随机值并妥善保管，丢失即无法解密既有凭据。
- **最小暴露面**：自托管时只对外暴露 site nginx（单 origin），后端服务不直接暴露端口。
