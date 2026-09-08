# Is this machine's Winsock stack usable right now?
#
# Runs after a failed Windows CI job, to answer the question that took a day to
# answer by hand: when the socket tests fail, is the host broken or are we? Five
# of 34 CI runs on 2026-09-07/08 failed on Windows with socket creation or
# connection errors -- an AF_UNIX dial to an ordinary temp path returning "an
# operation was attempted on something that is not a socket", a bind to the
# runner's Hyper-V adapter address failing the same way. None of it reproduced on
# real hardware: 51,200 AF_UNIX round trips, three full suite runs in one
# process, and 2,000 forced AppContainer launch failures all came back clean.
#
# So this does not fix anything and is not meant to. It states, in one line,
# whether the two primitives those tests need were working at the moment the job
# failed. A healthy verdict here means the failure was ours and is worth reading.
$ErrorActionPreference = 'Stop'
$failures = 0

Write-Host 'AF_UNIX (what the egress relay uses):'
# Windows PowerShell 5.1 runs on .NET Framework, which has no AF_UNIX endpoint
# type at all. Saying so beats reporting the host as broken: a check that cries
# wolf on the wrong shell is worse than no check, and this one exists precisely
# to tell a broken host from broken code.
if (-not ('System.Net.Sockets.UnixDomainSocketEndPoint' -as [type])) {
    Write-Host '  skip this shell has no AF_UNIX support (.NET Framework); re-run under pwsh 7 to check it'
} else {
$sockDir = Join-Path ([System.IO.Path]::GetTempPath()) ("nvxnet" + [System.Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $sockDir -Force | Out-Null
$sockPath = Join-Path $sockDir 's.sock'
try {
    $endpoint = [System.Net.Sockets.UnixDomainSocketEndPoint]::new($sockPath)
    $listener = [System.Net.Sockets.Socket]::new(
        [System.Net.Sockets.AddressFamily]::Unix,
        [System.Net.Sockets.SocketType]::Stream,
        [System.Net.Sockets.ProtocolType]::Unspecified)
    $listener.Bind($endpoint)
    $listener.Listen(1)
    $client = [System.Net.Sockets.Socket]::new(
        [System.Net.Sockets.AddressFamily]::Unix,
        [System.Net.Sockets.SocketType]::Stream,
        [System.Net.Sockets.ProtocolType]::Unspecified)
    $client.Connect($endpoint)
    $server = $listener.Accept()
    $client.Send([byte[]](1, 2, 3)) | Out-Null
    $buf = [byte[]]::new(3)
    $read = $server.Receive($buf)
    if ($read -ne 3) { throw "round trip moved $read bytes, expected 3" }
    Write-Host '  ok   bind, connect, accept and a three-byte round trip'
    $client.Dispose(); $server.Dispose(); $listener.Dispose()
} catch {
    Write-Host "  FAIL $($_.Exception.Message)"
    $failures++
} finally {
    Remove-Item $sockDir -Recurse -Force -ErrorAction SilentlyContinue
}
}

Write-Host 'TCP loopback (what the proxy and relay tests use):'
try {
    $l = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $l.Start()
    $port = ([System.Net.IPEndPoint]$l.LocalEndpoint).Port
    $c = [System.Net.Sockets.TcpClient]::new()
    $c.Connect([System.Net.IPAddress]::Loopback, $port)
    $a = $l.AcceptTcpClient()
    Write-Host "  ok   listen on 127.0.0.1:$port, connect and accept"
    $c.Dispose(); $a.Dispose(); $l.Stop()
} catch {
    Write-Host "  FAIL $($_.Exception.Message)"
    $failures++
}

Write-Host 'Interfaces this host advertises:'
foreach ($ip in [System.Net.Dns]::GetHostAddresses([System.Net.Dns]::GetHostName())) {
    if ($ip.AddressFamily -eq [System.Net.Sockets.AddressFamily]::InterNetwork) {
        $bindable = 'bindable'
        try {
            $probe = [System.Net.Sockets.TcpListener]::new($ip, 0)
            $probe.Start(); $probe.Stop()
        } catch {
            $bindable = "NOT bindable: $($_.Exception.Message)"
            $failures++
        }
        Write-Host "  $ip -> $bindable"
    }
}

if ($failures -gt 0) {
    Write-Host ''
    Write-Host "VERDICT: this host's network stack is unhealthy ($failures check(s) failed)."
    Write-Host 'The socket failures in the job above are the environment, not nvx.'
    exit 1
}
Write-Host ''
Write-Host 'VERDICT: the network stack is healthy; socket failures above are worth reading as real.'
