package bootstrap

import (
	"context"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

func serviceActive(ctx context.Context, m Manifest) bool {
	if m.Role == "client" {
		return false
	}
	switch runtime.GOOS {
	case "linux":
		return exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", m.Service).Run() == nil
	case "darwin":
		b, e := exec.CommandContext(ctx, "launchctl", "print", "system/"+m.Service).Output()
		return e == nil && strings.Contains(string(b), "state = running")
	case "windows":
		return windowsTask(ctx, m, "active", nil) == nil
	}
	return false
}
func serviceEnabled(ctx context.Context, m Manifest) bool {
	if m.Role == "client" {
		return false
	}
	if runtime.GOOS == "linux" {
		return exec.CommandContext(ctx, "systemctl", "is-enabled", "--quiet", m.Service).Run() == nil
	}
	return serviceActive(ctx, m)
}
func stopService(ctx context.Context, m Manifest) error {
	if m.Role == "client" {
		return nil
	}
	switch runtime.GOOS {
	case "linux":
		if serviceActive(ctx, m) {
			return run(ctx, "systemctl", "stop", m.Service)
		}
	case "darwin":
		if exec.CommandContext(ctx, "launchctl", "print", "system/"+m.Service).Run() == nil {
			return run(ctx, "launchctl", "bootout", "system/"+m.Service)
		}
	case "windows":
		return windowsTask(ctx, m, "stop", nil)
	}
	return nil
}
func startService(ctx context.Context, m Manifest, raw []byte) error {
	switch runtime.GOOS {
	case "linux":
		if e := run(ctx, "systemctl", "daemon-reload"); e != nil {
			return e
		}
		return run(ctx, "systemctl", "enable", "--now", m.Service)
	case "darwin":
		if e := run(ctx, "launchctl", "enable", "system/"+m.Service); e != nil {
			return e
		}
		return run(ctx, "launchctl", "bootstrap", "system", m.ServiceFile)
	case "windows":
		return windowsTask(ctx, m, "install", raw)
	}
	return nil
}
func restoreService(ctx context.Context, r installation) error {
	m := r.Manifest
	if m.Role == "client" {
		return nil
	}
	switch runtime.GOOS {
	case "linux":
		if e := run(ctx, "systemctl", "daemon-reload"); e != nil {
			return e
		}
		if r.WasEnabled {
			if e := run(ctx, "systemctl", "enable", m.Service); e != nil {
				return e
			}
		} else {
			exec.CommandContext(ctx, "systemctl", "disable", m.Service).Run()
		}
		if r.WasActive {
			return run(ctx, "systemctl", "start", m.Service)
		}
	case "darwin":
		if r.WasActive {
			return run(ctx, "launchctl", "bootstrap", "system", m.ServiceFile)
		}
	case "windows":
		raw, _ := json.Marshal(map[string]any{"task": m.Service, "xml": r.TaskXML, "was_active": r.WasActive})
		return windowsTask(ctx, m, "restore", raw)
	}
	return nil
}

const taskScript = `param($ManifestPath,$Operation)
$ErrorActionPreference='Stop'
$m=Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
$name=$m.task
$task=Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue
switch($Operation){
 'active' {if($null -eq $task -or $task.State -ne 'Running'){exit 1}}
 'stop' {if($task){Stop-ScheduledTask -TaskName $name}}
 'remove' {if($task){Unregister-ScheduledTask -TaskName $name -Confirm:$false}}
 'export' {if($task){
 if($task.Principal.LogonType -ne 'Interactive'){throw 'Only current-user interactive tasks can be replaced'}
 $u=[Security.Principal.WindowsIdentity]::GetCurrent()
 if($task.Principal.UserId -ne $u.Name -and $task.Principal.UserId -ne $u.User.Value){throw 'Task belongs to another user'}
 [System.IO.File]::WriteAllText($m.export_path,(Export-ScheduledTask -TaskName $name),[System.Text.UTF8Encoding]::new($false))
 }}
 'restore' {if($m.xml){Register-ScheduledTask -TaskName $name -Xml $m.xml -Force | Out-Null;if($m.was_active){Start-ScheduledTask -TaskName $name}}elseif($task){Unregister-ScheduledTask -TaskName $name -Confirm:$false}}
 'install' {
 $user=[Security.Principal.WindowsIdentity]::GetCurrent().Name
 $action=New-ScheduledTaskAction -Execute $m.executable -Argument $m.arguments
 $trigger=New-ScheduledTaskTrigger -AtLogOn -User $user
 $principal=New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
 $settings=New-ScheduledTaskSettingsSet -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
 Register-ScheduledTask -TaskName $name -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
 Start-ScheduledTask -TaskName $name
 }
}
`

func windowsTask(ctx context.Context, m Manifest, op string, raw []byte) error {
	dir, e := os.MkdirTemp("", "topophany-task-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	if len(raw) == 0 {
		raw, _ = json.Marshal(map[string]string{"task": m.Service})
	}
	for name, b := range map[string][]byte{"task.ps1": []byte(taskScript), "manifest.json": raw} {
		if e = secure.AtomicWrite(filepath.Join(dir, name), b, 0600); e != nil {
			return e
		}
	}
	return run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(dir, "task.ps1"), filepath.Join(dir, "manifest.json"), op)
}

func restrictWindowsDirectory(ctx context.Context, dir string) error {
	u, e := user.Current()
	if e != nil {
		return e
	}
	return run(ctx, "icacls.exe", dir, "/inheritance:r", "/grant:r", "*"+u.Uid+":(OI)(CI)F", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F")
}
func exportWindowsTask(ctx context.Context, m Manifest) (string, error) {
	dir, e := os.MkdirTemp("", "topophany-task-backup-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "task.xml")
	raw, _ := json.Marshal(map[string]string{"task": m.Service, "export_path": output})
	if e = windowsTask(ctx, m, "export", raw); e != nil {
		return "", e
	}
	b, e := secure.ReadBounded(output, 1<<20)
	if os.IsNotExist(e) {
		return "", nil
	}
	return string(b), e
}
