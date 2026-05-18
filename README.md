# ExternalDNS - Open Telekom Cloud DNS Webhook

This is an [ExternalDNS provider](https://github.com/kubernetes-sigs/external-dns/blob/master/docs/tutorials/webhook-provider.md) for [Open Telekom Cloud DNS server](https://www.t-cloud-public.com/de/produkte-services/core-services/domain-name-service).

## Installation

This webhook provider is run easiest as sidecar within the `external-dns` pod. This can be achieved using the 
[official `external-dns` Helm chart](https://kubernetes-sigs.github.io/external-dns/latest/charts/external-dns/)
and [its support for the `webhook` provider type]([https://kubernetes-sigs.github.io/external-dns/latest/charts/external-dns/#providers]).

> [!IMPORTANT]  
> Crucial information necessary for users to succeed.
Setting the `provider.name` to `webhook` allows configuration of the
`external-dns-openstack-webhook` via a few additional values:

```yaml
provider:
  name: webhook
  webhook:
    image:
      repository: ghcr.io/opentelekomcloud/external-dns-opentelekomcloud-webhook
      tag: 0.1.0
    extraVolumeMounts:
      - name: oscloudsyaml
        mountPath: /etc/openstack/
    resources: {}
extraVolumes:
  - name: oscloudsyaml
    secret:
      secretName: oscloudsyaml
```

The referenced `extraVolumeMount` points to a `Secret` containing a [`clouds.yaml` file](https://docs.openstack.org/python-openstackclient/latest/configuration/index.html#clouds-yaml),
which provides the Open Telekom Cloud credentials to the webhook provider.
`OS_*` environment variables are not supported for configuration, since the use of a `clouds.yaml` file offers more structure, capabilities and allows for better validation.
The one exception to this is `OS_CLOUD` for setting the name of the cloud in `clouds.yaml` to use.

The following example is a basic example of a `clouds.yaml` file, using `openstack` as the cloud name (the default used by this webhook):

```yaml
clouds:
  openstack:
    auth:
      auth_url: https://iam.eu-de.otc.t-systems.com:443/v3
      username: "<USER_NAME>"
      password: "<USER_PASSWORD>"
      user_domain_name: "<USER_DOMAIN_NAME>"
      project_name: "<PROJECT_NAME>"
    region_name: "eu-de"
    interface: "public"
    auth_type: "password"
```

> [!IMPORTANT]  
> Replace all values enclosed in <> with the corresponding values from your environment.

An existing file can be converted into a `Secret` via kubectl:

```shell
kubectl create secret generic oscloudsyaml --namespace external-dns --from-file=clouds.yaml
```

## Bugs or feature requests

This webhook certainly still contains bugs or lacks certain features.
In such cases, please raise a GitHub issue with as much detail as possible. PRs with fixes and features are also very welcome.

## Development

You need to clone and build ExternalDNS locally:

```bash
git clone https://github.com/kubernetes-sigs/external-dns
cd external-dns 
make build
```

In `/external-dns/build` create and run the script `run-local.sh`:

```bash
../build/external-dns \
  --source=service \
  --source=ingress \
  --source=crd \
  --annotation-prefix=external-dns.alpha.kubernetes.io/ \
  --ignore-ingress-tls-spec \
  --crd-source-apiversion=externaldns.k8s.io/v1alpha1 \
  --crd-source-kind=DNSEndpoint \
  --provider=webhook \
  --webhook-provider-url=http://localhost:8888 \
  --webhook-provider-read-timeout=180s \
  --webhook-provider-write-timeout=180s \
  --registry=txt \
  --txt-owner-id=local_dev \
  --policy=sync \
  --interval=30s \
  --kubeconfig ~/.kube/config
```

Then you need to start the webhoook locally, **out-of-cluster**. To run the webhook locally, you'll also require a [clouds.yaml](https://docs.openstack.org/python-openstackclient/pike/configuration/index.html#clouds-yaml) file in one of the standard-locations.
Also the name of the entry to be used has be given via `OS_CLOUD` environment variable (in the command line or in your IDE).
You can then start debugging the webhook server directly from your IDE of choice or run it out-of-cluster using:

```sh
go run cmd/webhook/main.go
```
