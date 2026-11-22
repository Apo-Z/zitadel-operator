# zitadel-operator

Opérateur Kubernetes (Go, controller-runtime) qui crée des **projets** et des **applications** dans [Zitadel](https://zitadel.com) à partir de manifests, et écrit les identifiants (client ID, client secret, clé JSON, issuer…) dans un **Secret** du namespace de l'application.

Cas d'usage typique : une application déployée par ArgoCD embarque, à côté de son `Deployment`, un `ZitadelApplication`. Au sync, le client OIDC est créé dans Zitadel et l'application consomme le Secret généré.

```
┌──────────── namespace "zitadel-operator" (équipe plateforme) ───────────┐
│ Secret zitadel-operator-credentials (clé du service account Zitadel)    │
│ ZitadelConnection "main" (cluster-scoped) ──► https://auth.example.com  │
└─────────────────────────────────────────────────────────────────────────┘
┌──────────── namespace "shop" (déployé par ArgoCD) ──────────────────────┐
│ ZitadelProject "shop"  ──►  projet + rôles dans Zitadel                 │
│ ZitadelApplication "shop-web" ──► client OIDC ──► Secret "shop-oidc"    │
│ Deployment "shop"  ◄── envFrom: secretRef shop-oidc                     │
└─────────────────────────────────────────────────────────────────────────┘
```

Les identifiants d'administration Zitadel restent dans le namespace de l'opérateur : les équipes applicatives ne les voient jamais.

## Ressources

| Kind | Portée | Rôle |
|---|---|---|
| `ZitadelConnection` | Cluster | URL de l'instance, identifiants (clé JSON de service account ou PAT), organisation par défaut, namespaces autorisés |
| `ZitadelProject` | Namespace | Projet Zitadel + rôles + options (role assertion / role check / project check) |
| `ZitadelApplication` | Namespace | Application `OIDC` (WEB, USER_AGENT, NATIVE) ou `API`, et le Secret généré |
| `ZitadelOrganization` | Cluster | Organisation (et organisation par défaut de l'instance), politiques de mot de passe / verrouillage / connexion, fournisseurs d'identité (Google), actions et leurs déclencheurs |
| `ZitadelSMTPProvider` | Cluster | Fournisseur e-mail SMTP de l'instance (activé par défaut) |

`ZitadelOrganization` et `ZitadelSMTPProvider` configurent l'**instance** : le service account de la
connexion doit être `IAM_OWNER` (par exemple le compte machine créé par `FirstInstance.Org.Machine`
du chart Zitadel). Exemple : `config/samples/03-organization.yaml`.

- Les éléments existants de même nom (organisation, fournisseur d'identité, action ; fournisseur SMTP
  de même description ou de même hôte) sont adoptés.
- Les politiques sont envoyées telles quelles (noms de champs de l'API Zitadel) : donner tous les
  champs, un champ absent serait remis à sa valeur zéro. Seuls les champs donnés sont comparés.
- Les secrets (identifiants des fournisseurs d'identité, mot de passe SMTP) sont lus dans des Secrets
  (`credentialsSecretRef`, `passwordSecretRef`) ; leur empreinte est gardée dans le statut et ils sont
  renvoyés à Zitadel quand ils changent (rotation), au plus tard à la resynchronisation suivante.
- Fournisseurs d'identité et actions retirés de la liste sont supprimés s'ils avaient été créés ou
  adoptés par la ressource ; les autres sont laissés. Sur un déclencheur, les actions non gérées par
  la ressource sont conservées.
- `deletionPolicy: Retain` par défaut : supprimer la ressource ne supprime rien dans Zitadel.

### Secret généré

Par défaut (sans `spec.secret.template`) :

| Clé | Contenu |
|---|---|
| `CLIENT_ID` | client ID |
| `CLIENT_SECRET` | client secret (méthodes `BASIC` / `POST`) |
| `key.json` | clé JSON de l'application (méthode `PRIVATE_KEY_JWT`) |
| `ISSUER` | URL de l'instance |
| `DISCOVERY_URL` | `<issuer>/.well-known/openid-configuration` |
| `PROJECT_ID`, `APP_ID`, `ORGANIZATION_ID` | identifiants Zitadel |

`spec.secret.template` permet de choisir les clés et leur format (templates Go) avec les champs `.ClientID .ClientSecret .KeyJSON .Issuer .DiscoveryURL .ProjectID .AppID .OrganizationID .AppName` :

```yaml
secret:
  name: grafana-oauth
  template:
    GF_AUTH_GENERIC_OAUTH_CLIENT_ID: "{{ .ClientID }}"
    GF_AUTH_GENERIC_OAUTH_CLIENT_SECRET: "{{ .ClientSecret }}"
    GF_AUTH_GENERIC_OAUTH_AUTH_URL: "{{ .Issuer }}/oauth/v2/authorize"
    GF_AUTH_GENERIC_OAUTH_TOKEN_URL: "{{ .Issuer }}/oauth/v2/token"
    GF_AUTH_GENERIC_OAUTH_API_URL: "{{ .Issuer }}/oidc/v1/userinfo"
  annotations:
    reloader.stakater.com/match: "true"
```

Le Secret a une `ownerReference` vers la `ZitadelApplication` : il est supprimé avec elle et ArgoCD l'affiche comme ressource enfant (il n'est pas pruné).

## Installation

### Helm (recommandé)

Le chart est publié en OCI sur GHCR à chaque tag `v*` (voir [CI](#ci--release)) :

```bash
helm install zitadel-operator oci://ghcr.io/apo-z/charts/zitadel-operator \
  --version 0.1.0 -n zitadel-operator --create-namespace
```

ou depuis le dépôt : `helm install zitadel-operator charts/zitadel-operator -n zitadel-operator --create-namespace`.

Principales values (voir [`values.yaml`](charts/zitadel-operator/values.yaml)) :

| Value | Défaut | Description |
|---|---|---|
| `image.repository` / `image.tag` | `ghcr.io/apo-z/zitadel-operator` / `v<appVersion>` | image |
| `crds.install` / `crds.keep` | `true` / `true` | installe les CRDs ; les conserve au `helm uninstall` |
| `credentials.create` + `credentials.keyJSON` | `false` | crée le Secret d'identifiants (préférer un Secret existant : external-secrets, sealed-secrets…) |
| `connections` | `[]` | `ZitadelConnection`s créées par le chart (hook post-install, compatible ArgoCD) |
| `metrics.serviceMonitor.enabled` | `false` | ServiceMonitor Prometheus |
| `leaderElection.enabled`, `replicaCount`, `resources`, `logLevel`… | | |

Exemple de values pour un Zitadel in-cluster :

```yaml
connections:
  - name: main
    url: https://auth.example.com
    internalURL: http://zitadel.zitadel.svc.cluster.local:8080
    credentialsSecretRef: {name: zitadel-operator-credentials, namespace: zitadel-operator, key: key.json}
    allowedNamespaces: ["team-*"]
```

### Kustomize

```bash
make docker-build docker-push IMG=registry.example.com/zitadel-operator:v0.1.0
cd config/manager && kustomize edit set image controller=registry.example.com/zitadel-operator:v0.1.0 && cd -
kubectl apply --server-side -k config/default
```

Le chart Helm comme `config/default` peuvent directement être la source d'une `Application` ArgoCD.

### Identifiants Zitadel

1. Dans la console Zitadel, créer un **service account** (machine user) et lui donner les droits selon l'un des modes décrits dans [Permissions du service account](#permissions-du-service-account).
2. Lui générer une clé de type **JSON** et la stocker :

```bash
kubectl -n zitadel-operator create secret generic zitadel-operator-credentials \
  --from-file=key.json=./service-account-key.json
```

Un Personal Access Token est aussi accepté (le contenu est détecté automatiquement) ; la clé JSON est recommandée.

3. Créer la connexion :

```yaml
apiVersion: zitadel.zitadel-operator.io/v1alpha1
kind: ZitadelConnection
metadata:
  name: main
spec:
  url: https://auth.example.com           # issuer public, écrit dans les Secrets
  # internalURL: http://zitadel.zitadel.svc.cluster.local:8080  # appels API in-cluster
  credentialsSecretRef:
    name: zitadel-operator-credentials
    namespace: zitadel-operator
    key: key.json
  allowedNamespaces: ["team-*", "shop"]   # optionnel, vide = tous
```

`internalURL` permet de joindre Zitadel par le service interne : l'opérateur envoie alors le header `Host` de `url` pour que Zitadel résolve la bonne instance.

```console
$ kubectl get zcon
NAME   URL                        ORGANIZATION   READY
main   https://auth.example.com   ACME           True
```

### Permissions du service account

Constat sur Zitadel v4.20.3 : au niveau organisation, **seul `ORG_OWNER`** donne les droits d'écriture sur les projets (`project.write`, `project.app.write`, `project.role.write`). `ORG_PROJECT_CREATOR` ne suffit pas : il permet de créer un projet, mais le créateur n'en devient pas membre et ne peut plus rien y faire (ni rôles, ni applications).

Deux modes sont possibles :

| Mode | Rôle(s) du service account | Ce que l'opérateur peut faire |
|---|---|---|
| **Complet** | `ORG_OWNER` sur l'organisation | créer / modifier / supprimer projets, rôles et applications |
| **Moindre privilège** | aucun rôle d'organisation, `PROJECT_OWNER` sur chaque projet à gérer | gérer rôles et applications des projets existants ; pas de création de projet |

**Mode moindre privilège**, pour chaque projet :

1. Un administrateur crée le projet dans Zitadel (ex. `shop`).
2. Dans le projet → *Members* (ou *Authorizations*) → ajouter le service account avec le rôle `PROJECT_OWNER`.
3. Côté Kubernetes, au choix :
   - un `ZitadelProject` portant **le même nom** (`spec.name: shop`) : il est adopté, ses rôles sont gérés. Mettre `deletionPolicy: Retain`, sinon supprimer la ressource supprime le projet (`PROJECT_OWNER` en a le droit) ;
   - ou directement des `ZitadelApplication` avec `spec.projectId` + `spec.connectionRef`.

La connexion est alors `Ready` avec le message *« Connected to Zitadel without org.read permission… »* et la colonne `ORGANIZATION` vide : l'opérateur détecte l'absence de `org.read` et récupère l'organisation via `/auth/v1/users/me`.

Pour limiter l'impact d'un `ORG_OWNER`, une alternative est une **organisation dédiée** aux projets gérés par l'opérateur (sans utilisateurs) : le service account y est `ORG_OWNER`, et les utilisateurs des autres organisations accèdent aux applications via des *project grants*.

## Utilisation avec ArgoCD

Ajouter les manifests au dépôt de l'application ([exemple complet](config/samples/02-application.yaml)) :

```yaml
apiVersion: zitadel.zitadel-operator.io/v1alpha1
kind: ZitadelProject
metadata:
  name: shop
  annotations:
    argocd.argoproj.io/sync-wave: "-1"
spec:
  connectionRef: {name: main}
  roles:
    - key: admin
    - key: customer
---
apiVersion: zitadel.zitadel-operator.io/v1alpha1
kind: ZitadelApplication
metadata:
  name: shop-web
  annotations:
    argocd.argoproj.io/sync-wave: "-1"
spec:
  projectRef: {name: shop}
  oidc:
    redirectUris: [https://shop.example.com/auth/callback]
    grantTypes: [AUTHORIZATION_CODE, REFRESH_TOKEN]
  secret:
    name: shop-oidc
```

Avec la sync-wave `-1` et le health check ci-dessous, ArgoCD attend que le Secret soit prêt avant de déployer le `Deployment` qui le consomme.

### Health check ArgoCD

Dans `argocd-cm` :

```yaml
data:
  resource.customizations.health.zitadel.zitadel-operator.io_*: |
    hs = { status = "Progressing", message = "Waiting for the operator" }
    if obj.status ~= nil and obj.status.conditions ~= nil then
      for _, c in ipairs(obj.status.conditions) do
        if c.type == "Ready" then
          hs.message = c.message
          if c.status == "True" then
            hs.status = "Healthy"
          elseif c.reason == "NamespaceForbidden" or c.reason == "InvalidSpec" or c.reason == "SecretConflict" then
            hs.status = "Degraded"
          end
        end
      end
    end
    return hs
```

## Comportement

- **Idempotence / adoption** : si le status est perdu (cluster reconstruit, ressource recréée), un projet ou une application portant le même nom est **adopté** au lieu d'être dupliqué. Pour une application adoptée sans Secret, un nouveau client secret est généré.
- **Drift** : toutes les 10 minutes (et à chaque modification), la configuration dans Zitadel est comparée à la spec et corrigée.
- **Secrets** : un client secret n'est lisible qu'à sa création dans Zitadel. Il est donc régénéré quand le Secret Kubernetes est supprimé, quand le template ou l'`authMethod` change, ou sur demande :
  ```bash
  kubectl annotate zapp shop-web zitadel.zitadel-operator.io/rotate-secret="$(date +%s)" --overwrite
  ```
  Pour `PRIVATE_KEY_JWT`, une nouvelle clé est créée et l'ancienne supprimée.
- **Rôles** : les rôles déclarés sont créés / mis à jour. Un rôle retiré de la spec est supprimé de Zitadel seulement s'il était géré par l'opérateur (`status.managedRoles`) ; les rôles créés à la main sont conservés.
- **Suppression** : `deletionPolicy: Delete` (défaut) supprime la ressource dans Zitadel (supprimer un projet supprime ses applications) ; `Retain` la conserve.
- **Changement de type** (`OIDC` ↔ `API`) ou de projet : l'application est recréée.
- **Projet existant non géré** : `spec.projectId` + `spec.connectionRef` au lieu de `spec.projectRef`.
- **Sécurité** : le Secret de sortie n'écrase jamais un Secret existant qui n'appartient pas à la `ZitadelApplication` (condition `SecretConflict`). Le cache de l'opérateur ne contient que les Secrets qu'il gère (label `app.kubernetes.io/managed-by=zitadel-operator`).

## Développement

```bash
make generate manifests   # deepcopy, CRDs, RBAC (controller-gen)
make helm                 # recopie CRDs + règles RBAC générées dans le chart
make helm-lint
make test                 # tests unitaires (client HTTP + contrôleurs avec un Zitadel en mémoire)
make test-e2e             # Zitadel réel (docker compose) + kube-apiserver (envtest)
make e2e-down
make run                  # lance l'opérateur avec le kubeconfig courant
```

Les tests e2e démarrent Zitadel v4.20.3 et vérifient de bout en bout : authentification JWT profile via `internalURL` et par PAT, validation CEL des CRDs, création projet/rôles/applications, validité du client secret et de la clé JSON auprès de Zitadel, mise à jour des redirect URIs, rotation, restriction de namespaces, suppression.

L'opérateur utilise l'API REST **management v1** de Zitadel (toujours disponible en v4, dépréciée au profit des API v2). Les appels sont isolés derrière l'interface `zitadel.API` (`internal/zitadel/types.go`) pour faciliter une migration.

## CI / release

`.github/workflows/ci.yaml` :

| Job | Déclencheur | Rôle |
|---|---|---|
| `test` | PR, push | vérifie que le code/CRDs/chart générés sont commités, gofmt, vet, tests unitaires (`-race`), build |
| `helm` | PR, push | `helm lint --strict` + rendu du chart |
| `e2e` | PR, push | `make test-e2e` (Zitadel en docker compose + envtest) |
| `image` | PR (build seul), push `main` et tags | image multi-arch `linux/amd64,linux/arm64` sur `ghcr.io/apo-z/zitadel-operator` |
| `chart` | tags `v*` | package du chart (version = tag) et push OCI sur `oci://ghcr.io/apo-z/charts` |

`.github/workflows/bump-chart.yaml` : à la publication d'une **release GitHub** `vX.Y.Z`, met `version` et `appVersion` du chart à `X.Y.Z` dans `Chart.yaml` et pousse le commit sur la branche par défaut.

Release : `git tag v0.1.0 && git push --tags` → image `ghcr.io/apo-z/zitadel-operator:v0.1.0` + chart `0.1.0` qui la référence.

### Arborescence

```
api/v1alpha1/              types des CRDs
cmd/main.go                manager
internal/zitadel/          client REST + authentification (JWT profile / PAT)
internal/controller/       réconciliateurs Connection / Project / Application
config/                    CRDs, RBAC, Deployment, exemples (Kustomize)
charts/zitadel-operator/   chart Helm (CRDs et RBAC synchronisés par `make helm`)
test/e2e/                  tests end-to-end
.github/workflows/         CI / release
```
