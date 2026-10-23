package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Replace the entire DACL, including explicit entries on existing children.
// Paths are literal arguments, never interpolated into PowerShell source.
// Verification also checks ownership: an unrelated owner can rewrite a DACL.
// Use .NET ACL APIs so an inherited PowerShell 7 module path cannot make
// Windows PowerShell load an incompatible Microsoft.PowerShell.Security module.
const windowsACLScript = `param($Directory,$Operation)
$ErrorActionPreference='Stop'
$current=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$sids=@($current,'S-1-5-18','S-1-5-32-544') | Select-Object -Unique
function Protect($path,$root){
 $item=Get-Item -LiteralPath $path -Force
 if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Reparse points are not installation paths'}
 $acl=$item.GetAccessControl([Security.AccessControl.AccessControlSections]'Access,Owner')
 if($sids -notcontains $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value){throw 'Installation path belongs to another user'}
 if($Operation -eq 'restrict'){
  $flags=if($item.PSIsContainer){'OICI'}else{''}
  $sddl='D:P'
  foreach($sid in $sids){$sddl+="(A;$flags;FA;;;$sid)"}
  # Persist only the DACL. Set-Acl can also try to write audit information,
  # which requires SeSecurityPrivilege even for the installation's owner.
  $restricted=if($item.PSIsContainer){[Security.AccessControl.DirectorySecurity]::new()}else{[Security.AccessControl.FileSecurity]::new()}
  $restricted.SetSecurityDescriptorSddlForm($sddl,[Security.AccessControl.AccessControlSections]::Access)
  $item.SetAccessControl($restricted)
  $acl=$item.GetAccessControl([Security.AccessControl.AccessControlSections]'Access,Owner')
 }
 if($root -and -not $acl.AreAccessRulesProtected){throw 'Installation DACL inherits unrelated access'}
 $rules=@($acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]))
 if($rules.Count -ne $sids.Count){throw 'Unexpected installation access rules'}
 foreach($sid in $sids){
  $matches=@($rules | Where-Object {$_.IdentityReference.Value -eq $sid})
  if($matches.Count -ne 1){throw 'Missing installation principal'}
  $rule=$matches[0]
  if($rule.AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow -or $rule.FileSystemRights -ne [Security.AccessControl.FileSystemRights]::FullControl -or $rule.PropagationFlags -ne [Security.AccessControl.PropagationFlags]::None){throw 'Unexpected installation access'}
  $expected=if($item.PSIsContainer){[Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'}else{[Security.AccessControl.InheritanceFlags]::None}
  if($rule.InheritanceFlags -ne $expected){throw 'Unexpected installation inheritance'}
 }
 if($item.PSIsContainer){
  foreach($child in (Get-ChildItem -LiteralPath $path -Force)){Protect $child.FullName $false}
 }
}
Protect $Directory $true
`

func windowsDirectoryACL(ctx context.Context, dir, operation string) error {
	tmp, err := os.MkdirTemp("", "topophany-acl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	script := filepath.Join(tmp, "acl.ps1")
	if err = secure.AtomicWrite(script, []byte(windowsACLScript), 0600); err != nil {
		return err
	}
	// Cold Windows PowerShell startup can consume the ordinary service
	// command's 15-second window on a busy host. Keep ACL work bounded while
	// leaving enough time for startup and recursive verification. A shorter
	// caller deadline still takes precedence, and failure still blocks use.
	return runWithTimeout(ctx, time.Minute, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, dir, operation)
}

func restrictWindowsDirectory(ctx context.Context, dir string) error {
	return windowsDirectoryACL(ctx, dir, "restrict")
}
