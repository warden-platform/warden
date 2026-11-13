# Contributing

Contributions should be based on the latest `master` branch and should not
contain credentials, kubeconfigs, internal hostnames, generated binaries,
`node_modules`, build output or local test reports.

Before submitting a change, run:

```bash
cd server
go test ./...
go vet ./...

cd ../web
npm ci
npm run build

cd ..
helm lint deploy/helm/pam-operator --set controlPlaneURL=https://pam.example.com
git diff --check
```

Security-sensitive changes should include negative tests covering authorization,
scope validation, replay protection, request-size limits or credential redaction
as applicable. Report vulnerabilities using `SECURITY.md`, not a public issue.
