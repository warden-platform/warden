# Security Policy

## Supported versions

Security fixes are applied to the latest release on the `master` branch. Older
images and Connector binaries should be upgraded before reporting a problem.

## Reporting a vulnerability

Do not open a public issue containing an exploit, credential, internal address,
or customer data. Contact the repository maintainers privately through the
hosting platform and include:

- affected version or commit;
- impacted component and deployment model;
- minimal reproduction steps;
- expected impact and any known mitigation.

Remove real credentials and personal data from evidence. The maintainers will
acknowledge the report, validate severity, prepare a fix and coordinate a public
advisory after affected deployments have had a reasonable upgrade window.

## Deployment boundary

HighTower brokers privileged infrastructure operations. Production deployments
must use TLS, independent randomly generated Secret files, least-privilege cloud
and Kubernetes identities, and restricted network egress. Development defaults
are not a production security baseline.
