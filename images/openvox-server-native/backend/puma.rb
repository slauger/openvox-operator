# Puma config for the CRuby compile backend.
#
# Catalog compilation is CPU-bound and CRuby holds the GIL, so parallelism comes
# from *processes*, not threads: one worker per core, a single thread each.
# preload_app! loads Puppet + the environment once in the master so forked
# workers share it copy-on-write (the CRuby analogue of the JRuby pool's warm
# caches). Scale further in Kubernetes with pod replicas + an HPA, not threads.
bind ENV.fetch('BACKEND_BIND', 'tcp://127.0.0.1:9140')
workers Integer(ENV.fetch('PUMA_WORKERS', '2'))
threads 1, 1
preload_app!
