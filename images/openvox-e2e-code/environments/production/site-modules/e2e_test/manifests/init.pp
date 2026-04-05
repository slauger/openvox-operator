class e2e_test {
  notify { 'e2e_test: catalog compiled and applied successfully': }

  file { '/tmp/e2e-test-marker':
    ensure  => file,
    content => "managed by openvox-operator e2e test\n",
  }
}
