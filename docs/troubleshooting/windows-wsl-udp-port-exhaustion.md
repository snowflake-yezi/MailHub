# Windows / WSL UDP 端口耗尽排障记录

> 日期：2026-07-09
> 场景：本机无法 `git pull` / `git ls-remote` GitHub，报 `Could not resolve hostname github.com`。
> 结论：WSL 的 Windows 侧 DeviceHost 组件所在 `dllhost.exe` 异常占用几乎全部 UDP 动态端口，导致 Windows DNS 查询失败。

---

## 1. 现象

Git 访问远程仓库失败：

```text
ssh: Could not resolve hostname github.com
fatal: Could not read from remote repository.
```

仓库配置本身正常：

```powershell
git remote -v
```

输出：

```text
origin  git@github.com:snowflake-yezi/MailHub.git (fetch)
origin  git@github.com:snowflake-yezi/MailHub.git (push)
```

分支跟踪关系也正常：

```powershell
git branch -vv
```

输出包含：

```text
* main cefc60b [origin/main] ...
```

所以第一判断不是 Git remote 配错，也不是分支上游丢失。

---

## 2. DNS 失败证据

默认 DNS 查询失败：

```powershell
Resolve-DnsName github.com
Resolve-DnsName baidu.com
Resolve-DnsName microsoft.com
```

均返回超时：

```text
This operation returned because the timeout period expired
```

`nslookup github.com` 也失败：

```text
Server:  UnKnown
Address:  8.8.8.8

*** UnKnown can't find github.com: No response from server
```

但 TCP DNS 曾可查询成功：

```powershell
Resolve-DnsName github.com -Server 8.8.8.8 -TcpOnly
```

返回：

```text
github.com A 20.205.243.166
```

同时 GitHub 解析出的 IP 端口可达：

```powershell
Test-NetConnection 20.205.243.166 -Port 22
Test-NetConnection 20.205.243.166 -Port 443
```

均 `TcpTestSucceeded: True`。

这说明不是 GitHub 主机不可达，而是 Windows 默认 DNS 查询链路出问题。

---

## 3. 本机网络配置

主网卡静态配置：

```text
IP:      192.168.0.5
Gateway: 192.168.0.1
DNS:     8.8.8.8
         114.114.114.114
DHCP:    No
```

路由表默认路由正常：

```text
0.0.0.0/0 -> 192.168.0.1
```

没有发现 VMware 虚拟网卡抢默认路由。

代理层也排除：

```powershell
netsh winhttp show proxy
```

输出：

```text
Direct access (no proxy server).
```

Git 全局配置没有 URL rewrite、代理或 `core.sshCommand`。

---

## 4. 关键系统日志

系统日志出现：

```text
Source: Tcpip
EventID: 4266
Message: 有关从全局 UDP 端口空间分配一个短端口号的请求失败，因为所有此类端口都在使用中。
```

这条日志非常关键：它说明 Windows 无法从 UDP 动态端口池里分配临时端口。

DNS 默认使用 UDP。若 UDP 临时端口分配失败，DNS 查询就会超时。

---

## 5. UDP 动态端口池

查看 UDP 动态端口范围：

```powershell
netsh int ipv4 show dynamicport udp
```

输出：

```text
Protocol udp Dynamic Port Range
---------------------------------
Start Port      : 49152
Number of Ports : 16384
```

也就是可用 UDP 临时端口大致为：

```text
49152-65535
共 16384 个
```

DNS 查询需要类似这样的临时端口：

```text
本机:52341 -> DNS服务器:53
DNS服务器:53 -> 本机:52341
```

如果临时端口池被占满，DNS 查询拿不到本机回信端口，就会失败。

---

## 6. 异常进程

统计 UDP 端口占用排行：

```powershell
netstat -ano -p udp | Select-String -Pattern "^\s*UDP" |
  ForEach-Object { ($_ -split '\s+')[-1] } |
  Group-Object |
  Sort-Object Count -Descending |
  Select-Object -First 10 Count,Name
```

异常时 PID `16612` 占用 `16312` 个 UDP 端口。

抽样可见它连续占用动态端口：

```text
UDP    0.0.0.0:49152    *:*    16612
UDP    0.0.0.0:49153    *:*    16612
UDP    0.0.0.0:49154    *:*    16612
...
```

查询进程：

```powershell
Get-CimInstance Win32_Process -Filter "ProcessId=16612" |
  Select ProcessId,ParentProcessId,Name,ExecutablePath,CommandLine,CreationDate
```

输出：

```text
ProcessId       : 16612
ParentProcessId : 1140
Name            : dllhost.exe
ExecutablePath  : C:\Windows\system32\DllHost.exe
CommandLine     : C:\Windows\system32\DllHost.exe /Processid:{17696EAC-9568-4CF5-BB8C-82515AAD6C09}
CreationDate    : 2026/6/27 14:40:42
```

父进程：

```text
ProcessId       : 1140
Name            : svchost.exe
CommandLine     : C:\Windows\system32\svchost.exe -k DcomLaunch -p
```

说明这是 DCOM 宿主拉起的 `dllhost.exe`。

---

## 7. AppID / COM 组件反查

`DllHost.exe /Processid:{...}` 里的 GUID 是 AppID。

查询 AppID：

```powershell
reg query "HKCR\AppID\{17696EAC-9568-4CF5-BB8C-82515AAD6C09}" /s
```

反查引用这个 AppID 的 COM 类：

```powershell
reg query HKCR\CLSID /f "{17696EAC-9568-4CF5-BB8C-82515AAD6C09}" /s
```

得到 3 个 CLSID：

```text
{16479D2E-F0C3-4DBA-BF7A-04FFF0892B07}
{60285AE6-AAF3-4456-B444-A6C2D0DEDA38}
{ABB755FC-1B86-4255-83E2-E5787ABCF6C2}
```

逐个查询：

```powershell
reg query "HKCR\CLSID\{16479D2E-F0C3-4DBA-BF7A-04FFF0892B07}" /s
reg query "HKCR\CLSID\{60285AE6-AAF3-4456-B444-A6C2D0DEDA38}" /s
reg query "HKCR\CLSID\{ABB755FC-1B86-4255-83E2-E5787ABCF6C2}" /s
```

结果：

```text
WslDeviceHost_Net
C:\Program Files\WSL\wsldevicehost.dll

WslDeviceHost_VirtioFs
C:\Program Files\WSL\wsldevicehost.dll

WslDeviceHost_VirtioPmem
C:\Program Files\WSL\wsldevicehost.dll
```

其中和本次 DNS / UDP 故障最相关的是：

```text
WslDeviceHost_Net
```

---

## 8. 根因链路

本次故障链路：

```text
WSL Windows 侧 DeviceHost 组件异常
  -> dllhost.exe / wsldevicehost.dll 占用大量 UDP 动态端口
  -> Windows UDP 动态端口池接近耗尽
  -> DNS Client 无法分配 UDP 临时端口
  -> github.com 等域名解析超时
  -> ssh / git 无法解析 GitHub remote
  -> git pull / git ls-remote 失败
```

更具体地说：

```text
异常进程 = dllhost.exe
承载组件 = C:\Program Files\WSL\wsldevicehost.dll
相关 COM 类 = WslDeviceHost_Net
```

---

## 9. 本次处理

经确认 PID `16612` 占用 `16312` 个 UDP 端口后，执行：

```powershell
Stop-Process -Id 16612 -Force
```

处理后验证：

```powershell
(netstat -ano -p udp | Select-String -Pattern "\s16612\s*$" | Measure-Object).Count
```

输出：

```text
0
```

DNS 恢复：

```powershell
Resolve-DnsName github.com
```

输出：

```text
github.com A 20.205.243.166
```

SSH GitHub 鉴权恢复：

```powershell
ssh -T git@github.com
```

输出：

```text
Hi snowflake-yezi! You've successfully authenticated, but GitHub does not provide shell access.
```

Git remote 恢复：

```powershell
git ls-remote origin HEAD
```

输出：

```text
cefc60b47bc796202cab77527b3f60163634d348 HEAD
```

---

## 10. 下次复发时先保留现场

这次因为已经结束了异常 `dllhost.exe`，无法继续追到 WSL 内部最初触发者。

下次如果再出现 DNS / GitHub 解析失败，不要第一时间杀进程。先采集现场：

### 10.1 确认 DNS 症状

```powershell
Resolve-DnsName github.com
Resolve-DnsName baidu.com
ssh -T git@github.com
```

### 10.2 统计 UDP 占用排行

```powershell
netstat -ano -p udp | Select-String -Pattern "^\s*UDP" |
  ForEach-Object { ($_ -split '\s+')[-1] } |
  Group-Object |
  Sort-Object Count -Descending |
  Select-Object -First 10 Count,Name
```

### 10.3 查异常 PID 信息

```powershell
$pid = <异常PID>
Get-CimInstance Win32_Process -Filter "ProcessId=$pid" |
  Select ProcessId,ParentProcessId,Name,ExecutablePath,CommandLine,CreationDate
```

### 10.4 查 WSL 状态

```powershell
wsl --status
wsl -l -v
```

### 10.5 在 WSL 内部查可能的触发进程

进入 WSL 后：

```bash
ss -uapn
ss -uan | wc -l
ps aux --sort=-%cpu | head
ps aux --sort=-%mem | head
```

如果用了 Docker：

```bash
docker ps
docker stats --no-stream
```

### 10.6 优先恢复方式

如果确认仍是 WSL DeviceHost 异常，优先尝试：

```powershell
wsl --shutdown
```

然后复测：

```powershell
Resolve-DnsName github.com
git ls-remote origin HEAD
```

如果 `wsl --shutdown` 无效，再结束异常 PID：

```powershell
Stop-Process -Id <异常PID> -Force
```

---

## 11. 学习要点

1. `Could not resolve hostname` 不等于 Git 配置错，首先要判断是不是 DNS。
2. DNS 默认走 UDP，UDP 也需要本机临时端口。
3. Windows 有 UDP 动态端口池，默认范围通常是 `49152-65535`。
4. 单个进程占用上万个 UDP 端口非常异常。
5. `DllHost.exe` 是 COM 宿主，不是具体业务名；要通过 `/Processid:{GUID}` 反查 AppID / CLSID。
6. WSL2 是 Linux 环境，但网络要经过 Windows 侧 WSL 宿主组件。
7. WSL 网络宿主异常可能影响 Windows 本机 DNS，不只影响 WSL 内部。
8. 下次复发时先采集现场，再恢复服务；否则无法追到 WSL 内部最初触发者。
