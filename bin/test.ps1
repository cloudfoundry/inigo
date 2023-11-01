$ErrorActionPreference = "Stop";
trap { $host.SetShouldExit(1) }

$env:GOROOT=(Get-ChildItem "C:\var\vcap\packages\golang-*-windows\go").FullName
$env:PATH= "$env:GOROOT\bin;$env:PATH"
$env:TMP = "C:\var\vcap\data\tmp"
$env:TEMP = "C:\var\vcap\data\tmp"

function Setup-DnsNames() {
  Write-Host "Setup-DnsNames"
    Add-Content -Path "C:\Windows\System32\Drivers\etc\hosts" -Encoding ASCII -Value "
    127.0.0.1 the-cell-id-1-0.cell.service.cf.internal
    127.0.0.1 the-cell-id-2-0.cell.service.cf.internal
    127.0.0.1 the-cell-id-3-0.cell.service.cf.internal
    127.0.0.1 the-cell-id-4-0.cell.service.cf.internal
    "
}

function Setup-TempDirContainerAccess() {
  Write-Host "Setup-TempDirContainerAccess"
    $rule = New-Object System.Security.AccessControl.FileSystemAccessRule("Users", "ReadAndExecute", "ContainerInherit, ObjectInherit", "None", "Allow")
    $acl = Get-Acl "$env:TEMP"
    $acl.AddAccessRule($rule)
    Set-Acl "$env:TEMP" $acl
}

function Setup-GardenRootfs() {
  Write-Host "Set-GardenRootfs"
    if (-not (Test-Path 'env:GARDEN_ROOTFS')) {
      throw "Please set GARDEN_ROOTFS environment variable"
    }
  $env:GROOTFS_BINPATH="$env:GARDEN_BINPATH"
  $env:GROOTFS_STORE_PATH="$env:GROOT_IMAGE_STORE"
    & "$env:GROOT_BINARY" --driver-store "$env:GROOTFS_STORE_PATH" pull "$env:GARDEN_ROOTFS"
    if ($LastExitCode -ne 0) {
      throw "Pulling $env:GARDEN_ROOTFS returned error code: $LastExitCode"
    }
}

function Setup-ContainerNetworking() {
  Write-Host "Setup-ContainerNetworking"
    Set-Content -Path "C:\var\vcap\data\winc-network.json" -Value '{
      "network_name": "winc-nat",
        "subnet_range": "172.30.0.0/22",
        "gateway_address": "172.30.0.1"
    }'

  & "$env:WINC_NETWORK_BINARY" --action delete --configFile "C:\var\vcap\data\winc-network.json"
    if ($LASTEXITCODE -ne 0) {
      throw "Deleting container network returned error code: $LastExitCode"
    }

  & "$env:WINC_NETWORK_BINARY" --action create --configFile "C:\var\vcap\data\winc-network.json"
    if ($LASTEXITCODE -ne 0) {
      throw "Creating container network returned error code: $LastExitCode"
    }

  Set-NetFirewallProfile -All -DefaultInboundAction Block -DefaultOutboundAction Allow -Enabled True
}

function Setup-GardenRunc() {
$env:GARDEN_BINPATH="$env:GARDEN_RUNC_RELEASE_PATH/bin"
mkdir -Force "$env:GARDEN_BINPATH"
cp "$env:WINIT_BINARY" "$env:GARDEN_BINPATH/init.exe"
cp "$env:NSTAR_BINARY" "$env:GARDEN_BINPATH/nstar.exe"
cp "$env:GROOT_BINARY" "$env:GARDEN_BINPATH/grootfs.exe"
cp "$env:GROOT_QUOTA_DLL" "$env:GARDEN_BINPATH/quota.dll"
cp "$env:WINC_BINARY" "$env:GARDEN_BINPATH/winc.exe"
cp "$env:WINC_NETWORK_BINARY" "$env:GARDEN_BINPATH/winc-network.exe"
$tarPath = (Get-Command tar).Source
cp "${tarPath}" "${env:GARDEN_BINPATH}/tar.exe"
}

function Setup-Database() {
  Write-Host "Setup-Database"

  $origCaFile="$env:DIEGO_RELEASE_PATH\src\code.cloudfoundry.org\inigo\fixtures\certs\sql-certs\server-ca.crt"
  $origCertFile="$env:DIEGO_RELEASE_PATH\src\code.cloudfoundry.org\inigo\fixtures\certs\sql-certs\server.crt"
  $origKeyFile="$env:DIEGO_RELEASE_PATH\src\code.cloudfoundry.org\inigo\fixtures\certs\sql-certs\server.key"

  $mysqlCertsDir = "$env:TEMP\mysql-certs" -replace '\\','\\'
  mkdir -Force $mysqlCertsDir

  $caFile="$mysqlCertsDir\\server-ca.crt"
  $certFile="$mysqlCertsDir\\server.crt"
  $keyFile="$mysqlCertsDir\\server.key"

  cp $origCaFile $caFile
  cp $origCertFile $certFile
  cp $origKeyFile $keyFile

  $mySqlBaseDir=(Get-ChildItem "C:\var\vcap\packages\mysql\mysql-*").FullName

  Set-Content -Path "$mySqlBaseDir\my.ini" -Encoding Ascii -Value "[mysqld]
basedir=$mySqlBaseDir
datadir=C:\\var\\vcap\\data\\mysql
ssl-cert=$certFile
ssl-key=$keyFile
ssl-ca=$caFile
max_connections=1000"

  Restart-Service Mysql
}

Setup-GardenRunc
Setup-GardenRootfs
Setup-ContainerNetworking
Setup-Database
Setup-DnsNames
Setup-TempDirContainerAccess

$env:CODE_CLOUDFOUNDRY_ORG_MODULE="$env:DIEGO_RELEASE_PATH/src/code.cloudfoundry.org"
$env:GUARDIAN_MODULE="$env:DIEGO_RELEASE_PATH/src/guardian"
$env:ROUTER_GOPATH="$env:ROUTING_RELEASE_PATH\src\code.cloudfoundry.org"
$env:ROUTING_API_GOPATH=$env:ROUTER_GOPATH
$env:APP_LIFECYCLE_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:AUCTIONEER_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:BBS_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:FILE_SERVER_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:HEALTHCHECK_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:LOCKET_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:REP_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:ROUTE_EMITTER_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:SSHD_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:SSH_PROXY_GOPATH=${env:CODE_CLOUDFOUNDRY_ORG_MODULE}
$env:GARDEN_GOPATH=${env:GUARDIAN_MODULE}

# used for routing to apps; same logic that Garden uses.
$ipAddressObject = Find-NetRoute -RemoteIPAddress "8.8.8.8" | Select-Object IpAddress
$ipAddress = $ipAddressObject.IpAddress
$env:EXTERNAL_ADDRESS="$ipAddress".Trim()

$timestamp="$((get-date).ToUniversalTime().ToString('yyyy-MM-ddTHH-mm-ss'))"
$logsDir="$env:TMP/inigo-logs-$timestamp"
echo "Log Dir: $logsDir"

mkdir -Force "$logsDir"
echo "Runing ginkgo in the background. Output will be at $logsDir/logs.txt"
Invoke-Expression "go run github.com/onsi/ginkgo/v2/ginkgo $args --output-dir $logsDir --json-report report.json" | Out-File "$logsDir/logs.txt" -Encoding ASCII -Width 1000
if ($LastExitCode -ne 0) {
  throw "tests failed"
}
