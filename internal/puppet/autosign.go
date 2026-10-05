package puppet

// AutosignPolicyPath is where openvox-autosign reads its policy by default. The
// operator mounts the rendered policy Secret as a directory at
// /etc/puppetlabs/puppet/autosign-policy, so the kubelet keeps the file in sync.
//
// puppetserver runs the autosign setting as a bare executable path and checks
// that the whole value exists as a file, so puppet.conf cannot pass the policy
// path as an argument. Binary and operator share this constant instead.
const AutosignPolicyPath = "/etc/puppetlabs/puppet/autosign-policy/autosign-policy.yaml"
