require 'puppet'
require 'json'

Puppet::Reports.register_report(:webhook) do
  desc "Forwards Puppet reports to openvox-report binary via stdin"

  def process
    report_data = self.to_data_hash.to_json
    binary = '/opt/puppetlabs/server/bin/openvox-report'

    unless File.executable?(binary)
      Puppet.warning("ReportProcessor webhook: binary not found at #{binary}")
      return
    end

    IO.popen([binary], 'r+') do |io|
      io.write(report_data)
      io.close_write
      output = io.read
      Puppet.debug("openvox-report: #{output}") unless output.empty?
    end

    unless $?.success?
      Puppet.err("ReportProcessor webhook: openvox-report exited with status #{$?.exitstatus}")
    end
  rescue => e
    Puppet.err("ReportProcessor webhook failed: #{e.message}")
  end
end
