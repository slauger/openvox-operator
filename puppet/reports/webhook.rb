require 'puppet'
require 'json'
require 'timeout'

Puppet::Reports.register_report(:webhook) do
  desc "Forwards Puppet reports to openvox-report binary via stdin"

  REPORT_TIMEOUT = 120

  def process
    report_data = self.to_data_hash.to_json
    binary = '/opt/puppetlabs/server/bin/openvox-report'

    unless File.executable?(binary)
      Puppet.warning("ReportProcessor webhook: binary not found at #{binary}")
      return
    end

    pid = nil
    output = ''

    Timeout.timeout(REPORT_TIMEOUT) do
      IO.popen([binary], 'r+') do |io|
        pid = io.pid
        io.write(report_data)
        io.close_write
        output = io.read
      end
    end

    Puppet.debug("openvox-report: #{output}") unless output.empty?

    unless $?.success?
      Puppet.err("ReportProcessor webhook: openvox-report exited with status #{$?.exitstatus}")
    end
  rescue Timeout::Error
    Process.kill('TERM', pid) if pid
    Puppet.err("ReportProcessor webhook: openvox-report timed out after #{REPORT_TIMEOUT}s")
  rescue => e
    Puppet.err("ReportProcessor webhook failed: #{e.message}")
  end
end
