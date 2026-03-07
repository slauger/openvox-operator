package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// reconcileConfigMaps creates or updates all ConfigMaps derived from the CR spec.
func (r *OpenVoxServerReconciler) reconcileConfigMaps(ctx context.Context, ovs *openvoxv1alpha1.OpenVoxServer) error {
	logger := log.FromContext(ctx)

	configMapName := fmt.Sprintf("%s-config", ovs.Name)

	data := map[string]string{
		"puppet.conf":     r.renderPuppetConf(ovs),
		"puppetdb.conf":   r.renderPuppetDBConf(ovs),
		"webserver.conf":  r.renderWebserverConf(ovs),
		"puppetserver.conf": r.renderPuppetserverConf(ovs),
		"product.conf":    "product: {\n    check-for-updates: false\n}\n",
	}

	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: ovs.Namespace}, cm)
	if errors.IsNotFound(err) {
		logger.Info("creating ConfigMap", "name", configMapName)
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: ovs.Namespace,
				Labels:    commonLabels(ovs),
			},
			Data: data,
		}
		if err := r.setOwnerReference(ovs, cm); err != nil {
			return err
		}
		return r.Create(ctx, cm)
	} else if err != nil {
		return err
	}

	// Update existing ConfigMap
	cm.Data = data
	return r.Update(ctx, cm)
}

// renderPuppetConf generates puppet.conf from the CR spec.
func (r *OpenVoxServerReconciler) renderPuppetConf(ovs *openvoxv1alpha1.OpenVoxServer) string {
	var sb strings.Builder
	sb.WriteString("[main]\n")
	sb.WriteString("confdir = /etc/puppetlabs/puppet\n")
	sb.WriteString("vardir = /opt/puppetlabs/puppet/cache\n")
	sb.WriteString("logdir = /var/log/puppetlabs/puppet\n")
	sb.WriteString("codedir = /etc/puppetlabs/code\n")
	sb.WriteString("rundir = /var/run/puppetlabs\n")
	sb.WriteString("manage_internal_file_permissions = false\n")

	if ovs.Spec.Puppet.ServerPort != 0 {
		sb.WriteString(fmt.Sprintf("serverport = %d\n", ovs.Spec.Puppet.ServerPort))
	}

	if ovs.Spec.Puppet.EnvironmentPath != "" {
		sb.WriteString(fmt.Sprintf("environmentpath = %s\n", ovs.Spec.Puppet.EnvironmentPath))
	}

	if ovs.Spec.Puppet.HieraConfig != "" {
		sb.WriteString(fmt.Sprintf("hiera_config = %s\n", ovs.Spec.Puppet.HieraConfig))
	}

	sb.WriteString("\n[server]\n")

	if ovs.Spec.Puppet.EnvironmentTimeout != "" {
		sb.WriteString(fmt.Sprintf("environment_timeout = %s\n", ovs.Spec.Puppet.EnvironmentTimeout))
	}

	if ovs.Spec.Puppet.Storeconfigs {
		sb.WriteString("storeconfigs = true\n")
		if ovs.Spec.Puppet.StoreBackend != "" {
			sb.WriteString(fmt.Sprintf("storeconfigs_backend = %s\n", ovs.Spec.Puppet.StoreBackend))
		}
	}

	if ovs.Spec.Puppet.Reports != "" {
		sb.WriteString(fmt.Sprintf("reports = %s\n", ovs.Spec.Puppet.Reports))
	}

	if ovs.Spec.CA.Enabled {
		if ovs.Spec.CA.TTL > 0 {
			sb.WriteString(fmt.Sprintf("ca_ttl = %d\n", ovs.Spec.CA.TTL))
		}
		if ovs.Spec.CA.Autosign != "" {
			sb.WriteString(fmt.Sprintf("autosign = %s\n", ovs.Spec.CA.Autosign))
		}
	}

	if ovs.Spec.CA.DNSAltNames != nil {
		sb.WriteString(fmt.Sprintf("dns_alt_names = %s\n", strings.Join(ovs.Spec.CA.DNSAltNames, ",")))
	}

	if ovs.Spec.CA.Certname != "" {
		sb.WriteString(fmt.Sprintf("certname = %s\n", ovs.Spec.CA.Certname))
	}

	// Extra config entries
	for k, v := range ovs.Spec.Puppet.ExtraConfig {
		sb.WriteString(fmt.Sprintf("%s = %s\n", k, v))
	}

	return sb.String()
}

// renderPuppetDBConf generates puppetdb.conf.
func (r *OpenVoxServerReconciler) renderPuppetDBConf(ovs *openvoxv1alpha1.OpenVoxServer) string {
	if !ovs.Spec.PuppetDB.Enabled || len(ovs.Spec.PuppetDB.ServerURLs) == 0 {
		return "[main]\nserver_urls = https://openvoxdb:8081\nsoft_write_failure = true\n"
	}

	return fmt.Sprintf("[main]\nserver_urls = %s\nsoft_write_failure = true\n",
		strings.Join(ovs.Spec.PuppetDB.ServerURLs, ","))
}

// renderWebserverConf generates webserver.conf for puppetserver.
func (r *OpenVoxServerReconciler) renderWebserverConf(ovs *openvoxv1alpha1.OpenVoxServer) string {
	port := int32(8140)
	if ovs.Spec.Puppet.ServerPort != 0 {
		port = ovs.Spec.Puppet.ServerPort
	}

	return fmt.Sprintf(`webserver: {
    ssl-host: 0.0.0.0
    ssl-port: %d
    ssl-cert: /etc/puppetlabs/puppet/ssl/certs/puppet.pem
    ssl-key: /etc/puppetlabs/puppet/ssl/private_keys/puppet.pem
    ssl-ca-cert: /etc/puppetlabs/puppet/ssl/certs/ca.pem
    ssl-crl-path: /etc/puppetlabs/puppet/ssl/crl.pem
}
`, port)
}

// renderPuppetserverConf generates puppetserver.conf.
func (r *OpenVoxServerReconciler) renderPuppetserverConf(ovs *openvoxv1alpha1.OpenVoxServer) string {
	maxActiveInstances := int32(2)
	if ovs.Spec.Compilers.MaxActiveInstances > 0 {
		maxActiveInstances = ovs.Spec.Compilers.MaxActiveInstances
	}

	return fmt.Sprintf(`jruby-puppet: {
    ruby-load-path: [/opt/puppetlabs/puppet/lib/ruby/vendor_ruby]
    gem-home: /opt/puppetlabs/server/data/puppetserver/jruby-gems
    gem-path: [${jruby-puppet.gem-home}, "/opt/puppetlabs/server/data/puppetserver/vendored-jruby-gems", "/opt/puppetlabs/puppet/lib/ruby/vendor_gems"]
    master-conf-dir: /etc/puppetlabs/puppet
    master-code-dir: /etc/puppetlabs/code
    master-var-dir: /opt/puppetlabs/server/data/puppetserver
    master-run-dir: /var/run/puppetlabs/puppetserver
    master-log-dir: /var/log/puppetlabs/puppetserver
    max-active-instances: %d
    max-requests-per-instance: 0
}

http-client: {
}

profiler: {
}

dropsonde: {
    enabled: false
}
`, maxActiveInstances)
}

// commonLabels returns the standard labels for all managed resources.
func commonLabels(ovs *openvoxv1alpha1.OpenVoxServer) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "openvoxserver",
		"app.kubernetes.io/instance":   ovs.Name,
		"app.kubernetes.io/managed-by": "openvox-operator",
	}
}
