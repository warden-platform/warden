---
name: pam-resource-registration
description: Register Jenkins, Grafana, database, and cloud Integrations into Warden using a purpose-bound Registration Key. Use for Multica requests to inspect registration context, create or update Warden projects and Integrations, or configure Integration project/environment scope without global admin credentials.
---

# Warden resource registration

Use `scripts/pam-registration.sh`. Credentials are injected as `WARDEN_API_ENDPOINT` and `WARDEN_REGISTRATION_API_KEY`.

## Hard boundary

- Require a `pam_rgk_` Registration Key. Never request, accept, read, copy, or use `WARDEN_ADMIN_API_KEY`, admin passwords, login cookies, ordinary `pam_agk_` Agent Keys, Connector task keys, or signing keys.
- Never call policy, approval, execution, audit, Principal/credential, pairing, Connector mutation, resource mutation, or delete APIs.
- Never access the Warden database, Kubernetes Secrets, local `.secrets`, source-code credentials, browser storage, or target-system Secret values.
- Integration requests contain only a `credentialRef`; the referenced Secret must already exist in the selected Connector runtime. If it does not exist, stop and give the user the exact Secret name and target namespace/path to prepare through an authorized credential workflow.
- Do not invoke direct Jenkins, Grafana, database, cloud, SSH, or Kubernetes admin APIs from this Skill.
- Do not broaden `scope.allowAll` without explicit user confirmation. Prefer explicit Jobs, Dashboards, schemas, services, projects, or Regions.

## Workflow

1. Run `scripts/pam-registration.sh preflight`. A `401` means the Key is invalid or belongs to another Warden Endpoint. A `403` means the provided credential is not a Registration Key or lacks the fixed registration Scope. Never ask to add `pam:admin`.
2. List Connectors, projects, and Integrations. Select an ACTIVE Connector that actually hosts the pre-created credential reference.
3. Reuse an existing project by exact name. Create a project only when the user explicitly named a missing project; do not rename existing projects implicitly.
4. Validate the Integration request locally: protocol, HTTPS/host endpoint, non-secret `credentialRef`, project, environment, and the narrowest supported scope.
5. Create or update the Integration. Never delete and recreate an existing Integration to bypass a conflict.
6. Read the Integration project scope, then update only the user-requested default project, environment, allowed projects, and routing rules.
7. Re-read the Integration and report IDs, Connector, endpoint, credential reference name, effective project/environment, scope, and health. Do not claim success until the API confirms the saved object.

## Commands

```bash
scripts/pam-registration.sh preflight
scripts/pam-registration.sh list-connectors
scripts/pam-registration.sh list-projects
scripts/pam-registration.sh list-integrations
scripts/pam-registration.sh list-connector-integrations CONNECTOR_ID
scripts/pam-registration.sh create-project NAME DESCRIPTION
scripts/pam-registration.sh update-project PROJECT_ID NAME DESCRIPTION
scripts/pam-registration.sh create-integration CONNECTOR_ID REQUEST_JSON_FILE
scripts/pam-registration.sh update-integration CONNECTOR_ID INTEGRATION_ID REQUEST_JSON_FILE
scripts/pam-registration.sh get-project-scope INTEGRATION_ID
scripts/pam-registration.sh set-project-scope INTEGRATION_ID REQUEST_JSON_FILE
```

Create request JSON files with mode `0600` in a temporary directory, pass them to the script, and remove them immediately. They may contain endpoint/user/credential reference/scope metadata, but never a password, Token, AK/SK, DSN password, Authorization header, or Secret value.

## Allowed Warden API surface

| Access | Endpoints |
|---|---|
| Read | `GET /connectors`, `/projects`, `/integrations`, `/connectors/{id}/integrations`, `/integrations/{id}/project-scope` |
| Write | `POST /projects`, `PUT /projects/{id}`, `POST /connectors/{id}/integrations`, `PUT /connectors/{id}/integrations/{id}`, `PUT /integrations/{id}/project-scope` |

Everything else is forbidden even if a malformed server configuration appears to allow it. Return a concise registration summary plus any missing external credential preparation; redact credentials, authorization data, signed URLs, cookies, and unrelated target metadata.
