# Native CRuby compile backend for the openvox-server-native image.
#
# This is the Rack equivalent of openvox-server's compiler.rb: it drives the very
# same Puppet indirections (node = plain, catalog = compiler) the JVM server drives
# through its JRuby pool -- only here it runs on plain CRuby (MRI). The Go edge in
# front terminates mTLS and authorizes; this backend only ever sees localhost
# traffic and trusts the X-Client-* headers the edge sets.

require 'json'
require 'rack'
require 'puppet'

CODEDIR = ENV.fetch('CODEDIR', '/etc/puppetlabs/code')

Puppet.initialize_settings(%W[
  --codedir #{CODEDIR}
  --environmentpath #{CODEDIR}/environments
  --vardir #{ENV.fetch('VARDIR', '/opt/puppetlabs/server/data/puppetserver')}
  --confdir #{ENV.fetch('CONFDIR', '/etc/puppetlabs/puppet')}
  --rundir #{ENV.fetch('RUNDIR', '/var/run/puppetlabs')}
  --logdir #{ENV.fetch('LOGDIR', '/var/log/puppetlabs')}
  --autosign false
])
Puppet::Node.indirection.terminus_class = :plain
Puppet::Resource::Catalog.indirection.terminus_class = :compiler

# GET /puppet/v3/catalog/<certname> -- the endpoint agents hit for their catalog.
CATALOG = %r{\A/puppet/v3/catalog/([^/]+)\z}

run(lambda do |env|
  req = Rack::Request.new(env)

  if req.path_info == '/status/v1/simple'
    return [200, { 'content-type' => 'text/plain' }, ["running\n"]]
  end

  m = CATALOG.match(req.path_info)
  unless m
    return [404, { 'content-type' => 'text/plain' }, ["not found\n"]]
  end

  certname    = Rack::Utils.unescape(m[1])
  environment = req.params['environment'] || 'production'

  # The edge already proved the client owns this certname (auth.conf allow "$1").
  facts_values = {}
  raw = req.body.read.to_s
  unless raw.empty?
    parsed = (JSON.parse(raw) rescue {})
    facts_values = parsed['values'] || parsed
  end
  facts = Puppet::Node::Facts.new(certname, facts_values)

  begin
    node    = Puppet::Node.indirection.find(certname, environment: environment, facts: facts)
    catalog = Puppet::Parser::Compiler.compile(node)
    [200, { 'content-type' => 'application/json' }, [catalog.to_data_hash.to_json]]
  rescue => e
    Puppet.err("catalog compile failed for #{certname}: #{e.class}: #{e.message}")
    [500, { 'content-type' => 'text/plain' },
     ["compile error: #{e.class}: #{e.message}\n"]]
  end
end)
