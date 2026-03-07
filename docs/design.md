# OpenVox Operator — Architektur & Kubernetes-Design

## Context

### Probleme mit bestehenden Container-Ansätzen

1. **ezbake-Legacy**: Upstream OpenVox Server nutzt ezbake für Packaging. Generiert Init-Scripts die als root starten und per `runuser`/`su`/`sudo` auf den puppet-User wechseln. Bricht rootless Container und OpenShift random UIDs.

2. **Doppelte Ruby-Installation**: Der Server braucht JRuby (eingebettet im JAR) für den Runtime. Die bisherigen Container installieren zusätzlich System Ruby + openvox Gem nur damit die entrypoint.d Scripts `puppet config set/print` aufrufen können. Das ist unnötig — diese Scripts schreiben nur INI-Config-Dateien die in K8s per ConfigMap kommen.

3. **Docker-Logik im K8s-Kontext**: ~15 entrypoint.d Scripts übersetzen ENV-Vars in Config-Dateien. Das ist ein Docker-Pattern. In K8s erledigt das der Operator via ConfigMaps/Secrets.

4. **Keine Rollentrennung**: Container muss selbst entscheiden ob CA oder Compiler. In K8s macht der Operator die Orchestrierung.

5. **chown/chmod Probleme**: `openvoxserver-ca` Gem ruft `FileUtils.chown` auf — failed rootless. Muss gepatcht werden. Besser: Upstream-Fix in openvox vorschlagen (`manage_internal_file_permissions` konsequent umsetzen).

### Was in OpenVox upstream verbessert werden könnte

- `openvoxserver-ca` Gem: `chown`/`lchown` Calls sollten `manage_internal_file_permissions` respektieren
- `symlink_to_old_cadir` sollte optional/konfigurierbar sein
- Tarball sollte puppet gem Dependencies für JRuby vollständig mitbringen (aktuell muss `puppetserver gem install openvox` separat laufen)

## CRD Design

### OpenVoxServer (Haupt-CRD)

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: OpenVoxServer
metadata:
  name: production
  namespace: openvox
spec:
  # Image
  image:
    repository: ghcr.io/slauger/openvoxserver
    tag: "8.12.1"

  # CA Konfiguration (Single Instance)
  ca:
    enabled: true                     # false = externe CA verwenden
    autosign: true                    # true/false/script-path
    ttl: 157680000                    # 5 Jahre in Sekunden
    allowSubjectAltNames: true
    certname: "puppet"
    dnsAltNames:
      - puppet
      - puppet-ca
      - puppet-ca.openvox.svc
    storage:
      size: 1Gi
      storageClass: ""                # Default StorageClass
    resources:
      requests: { memory: "1Gi", cpu: "500m" }
      limits:   { memory: "2Gi", cpu: "1500m" }
    javaArgs: "-Xms512m -Xmx1024m"
    # Optional: Intermediate CA (Zertifikate per Secret mounten)
    intermediateCA:
      enabled: false
      secretName: ""                  # Secret mit ca.pem, key.pem, crl.pem

  # Compiler Konfiguration (Scalable)
  compilers:
    replicas: 1                       # 0 = nur CA Server, kein separater Compiler
    autoscaling:
      enabled: false
      minReplicas: 1
      maxReplicas: 5
      targetCPU: 75
    dnsAltNames:
      - puppet
      - puppet.openvox.svc
    resources:
      requests: { memory: "1Gi", cpu: "500m" }
      limits:   { memory: "2Gi", cpu: "1500m" }
    javaArgs: "-Xms512m -Xmx1024m"
    maxActiveInstances: 2             # JRuby Instanzen pro Compiler

  # Puppet Konfiguration (wird zu ConfigMap)
  puppet:
    serverport: 8140
    environmentpath: /etc/puppetlabs/code/environments
    environmentTimeout: unlimited
    hieraConfig: "$confdir/hiera.yaml"
    storeconfigs: true
    storebackend: puppetdb
    reports: puppetdb
    # Zusätzliche puppet.conf Einträge
    extraConfig: {}

  # PuppetDB Verbindung (extern bereitgestellt)
  puppetdb:
    enabled: true
    serverUrls:
      - https://openvoxdb:8081

  # Code Deployment
  code:
    # r10k als initContainer oder CronJob
    r10k:
      enabled: false
      image:
        repository: ghcr.io/slauger/r10k
        tag: "latest"
      repository: ""                  # Git URL
      schedule: "*/5 * * * *"         # CronJob Schedule
    # Alternativ: PVC für Code (manuell befüllt)
    volume:
      existingClaim: ""
      size: 5Gi

status:
  phase: Running                      # Pending | CASetup | WaitingForCA | Running | Error
  caReady: true
  caSecretName: production-ca
  compilersReady: 2
  compilersDesired: 2
  conditions:
    - type: CAInitialized
      status: "True"
    - type: CAServerReady
      status: "True"
    - type: CompilersReady
      status: "True"
```

## Komponenten im Detail

### 1. Operator (Controller)

- **Sprache**: Go (kubebuilder/controller-runtime)
- **Reconciliation Loop**:
  1. Liest `OpenVoxServer` CR
  2. Generiert ConfigMaps aus `spec.puppet.*` (puppet.conf, puppetdb.conf, webserver.conf, etc.)
  3. Wenn `ca.enabled` und kein CA Secret existiert → erstellt CA Setup Job
  4. Wartet auf Job-Completion → liest CA Zertifikate → erstellt CA Secret
  5. Erstellt CA StatefulSet (replicas: 1, PVC für CA-Daten)
  6. Wartet auf CA readiness (Status-Endpoint)
  7. Erstellt Compiler Deployment (replicas: N, CA Secret gemountet)
  8. Compiler-Pods bootstrappen sich per `puppet ssl bootstrap` beim CA Server

### 2. CA Setup Job

Ein einmaliger Job der `puppetserver ca setup` ausführt und die CA-Zertifikate in einem PVC speichert.

### 3. CA Server (StatefulSet, replicas: 1)

- Mountet CA-Daten PVC
- Mountet puppet.conf, puppetdb.conf, webserver.conf als ConfigMap
- CA Service enabled (`ca.cfg`)
- Kein System Ruby nötig — Config kommt per ConfigMap
- Entrypoint: direkt `java` (kein entrypoint.d)
- Liveness/Readiness: TCP-Check auf Port 8140

### 4. Compiler (Deployment, replicas: N)

- CA Service disabled
- Mountet CA cert aus Secret (read-only)
- InitContainer: `puppet ssl bootstrap --server puppet-ca`
- Code via PVC oder r10k initContainer
- Entrypoint: direkt `java`
- HPA möglich

### 5. Code Sync (r10k)

- **initContainer** auf jedem Compiler-Pod: `r10k deploy environment`
- **Optional CronJob**: periodisches Code-Update
- **Separates Image**: nur Ruby + r10k, kein puppetserver
- Code-PVC: ReadWriteMany wenn mehrere Compiler

## cert-manager Integration

Puppet nutzt **standard X.509 Zertifikate** (RSA/EC Keys, PEM-Format). Die CA ist ein normales X.509 CA-Zertifikat. cert-manager **könnte** die Root-CA erstellen.

**Aber**: Puppet CSRs haben eigene OID-Extensions (`1.3.6.1.4.1.34380.*` — pp_uuid, pp_instance_id, pp_auth_role). Diese werden vom Puppet CA Service beim Signing verarbeitet. cert-manager kann diese nicht erzeugen.

| Ansatz | Vorteil | Nachteil |
|--------|---------|----------|
| **Puppet CA standalone** (Default) | Einfach, funktioniert out-of-the-box | Eigene CA-Verwaltung nötig |
| **cert-manager Root → Puppet Intermediate** | Root-CA Lifecycle via cert-manager | Komplexer Setup |
| **cert-manager für Server-TLS, Puppet CA für Agents** | Trennung von Concerns | Zwei PKI-Systeme |

**Empfehlung**: Puppet CA standalone als Default. cert-manager Integration als optionales Feature für `spec.ca.intermediateCA`.

## Container Image (K8s-first)

### Was rausfliegt

- Alle entrypoint.d Scripts (Config kommt per ConfigMap)
- System Ruby openvox Gem (kein `puppet config set/print` mehr nötig)
- Gemfile / bundle install / ruby-devel / gcc / make
- Die ganze ENV-Var → Config Übersetzungslogik
- Docker-Compose Support

### Was bleibt

- UBI9 + JDK 17
- Tarball-Installation (puppet-server-release.jar, CLI tools, vendored JRuby gems)
- PuppetDB-Termini
- `puppetserver gem install openvox` (JRuby)
- openvoxserver-ca Patch (chown/symlink)
- OpenShift random-UID Pattern (chgrp 0, chmod g=u, SGID)
- Schlankes Entrypoint: direkt `java` starten

### Kein Docker-Compose Support

Docker-Compose wird **nicht** unterstützt. Die zwei Ansätze beißen sich:
- K8s: Config per ConfigMap/Secret, CA per Job → schlankes Image ohne entrypoint.d
- Docker-Compose: braucht entrypoint.d Scripts die ENV-Vars → Config übersetzen

**Lokales Testen**: `kind` oder `minikube` + die gleichen K8s Manifests.

## Phasen

### Phase 1: Container Image
- [x] Rootless Containerfile (UBI9, Tarball, kein ezbake)
- [ ] System Ruby entfernen (kein Gemfile/bundle install)
- [ ] Entrypoint vereinfachen (direkt java, keine entrypoint.d Scripts)
- [ ] Image bauen und testen

### Phase 2: Kubernetes Manifests
- [ ] Beispiel-ConfigMaps für puppet.conf, webserver.conf, etc.
- [ ] StatefulSet für CA Server
- [ ] Deployment für Compiler
- [ ] Job für CA Setup
- [ ] Services (puppet-ca, puppet)

### Phase 3: Operator
- [x] Go Projekt initialisieren (go.mod, cmd/main.go)
- [x] CRD `OpenVoxServer` definieren (api/v1alpha1/)
- [x] Controller: ConfigMap Generation
- [x] Controller: CA Job Lifecycle
- [x] Controller: CA StatefulSet Management
- [x] Controller: Compiler Deployment Management
- [ ] CRD YAML generieren
- [ ] RBAC Manifests

### Phase 4: Extras
- [ ] r10k Integration (initContainer / CronJob)
- [ ] HPA für Compiler
- [ ] cert-manager Intermediate CA Support
- [ ] OLM Bundle für OpenShift
