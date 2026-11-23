<#
.SYNOPSIS
  Shows what a meeting app's windows expose through UI Automation: the
  question behind docs/research/call-app-names.md, answered on a real call.

.DESCRIPTION
  Reads each visible top-level window of the given process once (or every
  -Every seconds) with one cached subtree request, as flit's reader would,
  and prints how long the read took, how many elements came back by control
  type, and the signals that matter for naming speakers: call controls
  (Leave, Hang up, hangup-button), labels that say someone is speaking, a
  live-captions area, and lists that look like the participants panel.

  Texts are redacted by default: every text becomes its length, "<12>",
  except a short list of the apps' own words. -Names shows them; the output
  then holds participants' names (and captions, if on): keep it on this PC.

  Run it during a call, with the participants panel open and closed, and
  with captions on and off. It only reads; it changes nothing in the app.

.EXAMPLE
  .\scripts\uia-probe.ps1 -Process ms-teams
  .\scripts\uia-probe.ps1 -Process Zoom -Every 1 -Seconds 30
  .\scripts\uia-probe.ps1 -Process msedge -Tree -Out probe.txt
#>
param(
    [Parameter(Mandatory = $true)] [string] $Process, # ms-teams, Zoom, chrome, msedge, webex
    [double] $Every = 0,      # seconds between reads; 0 reads once
    [double] $Seconds = 20,   # how long to repeat
    [int] $Budget = 4000,     # most elements per window, as flit's reader
    [switch] $Tree,           # print the (redacted) tree, not just the summary
    [switch] $Names,          # don't redact texts
    [string] $Out = ''        # also write everything to this file
)

Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes

$appWords = @('leave', 'leave call', 'hang up', 'end call', 'mute', 'unmute', 'mute mic', 'unmute mic', 'camera',
    'participants', 'people', 'attendees', 'chat', 'more', 'share', 'raise', 'react', 'captions', 'live captions',
    'speaking', 'active speaker', 'talking:', 'muted', 'unmuted', 'organizer', 'presenter', 'guest', 'host',
    'microsoft teams', 'zoom meeting', 'meeting', 'meet', 'you', '(you)', '(me)')
$callControl = '^(leave|leave call|hang up|end call|mute mic|unmute mic|verlassen|auflegen)$'
$speakingRe = '(speaking|active speaker|talking|is talking|spricht|sprechen)'
$captionRe = '(caption|untertitel|transcript)'
$rosterRe = '^(meeting )?(participants|attendees|people|in this meeting|teilnehmer|personen)'

$lines = New-Object System.Collections.Generic.List[string]
function Say([string] $s) { $lines.Add($s); Write-Output $s }

function Redact([string] $s) {
    if ([string]::IsNullOrWhiteSpace($s)) { return '' }
    $t = ($s -replace '\s+', ' ').Trim()
    if ($Names) { if ($t.Length -gt 120) { return $t.Substring(0, 120) + '…' } return $t }
    $label = ($t -replace '\s*\([^)]*\)\s*$', '').Trim().ToLowerInvariant()
    if ($appWords -contains $label) { return $t }
    return "<$($t.Length)>"
}

$cr = New-Object System.Windows.Automation.CacheRequest
foreach ($p in @([System.Windows.Automation.AutomationElement]::ControlTypeProperty,
        [System.Windows.Automation.AutomationElement]::NameProperty,
        [System.Windows.Automation.AutomationElement]::AutomationIdProperty,
        [System.Windows.Automation.AutomationElement]::ClassNameProperty,
        [System.Windows.Automation.AutomationElement]::HelpTextProperty)) { $cr.Add($p) }
$cr.TreeScope = [System.Windows.Automation.TreeScope]::Subtree
$cr.TreeFilter = [System.Windows.Automation.Automation]::RawViewCondition
$cr.AutomationElementMode = [System.Windows.Automation.AutomationElementMode]::None

function Read-Window($win) {
    $stats = @{ n = 0; depth = 0; roles = @{}; controls = 0; speaking = 0; captions = 0; rosters = 0; webEmpty = 0 }
    $treeLines = New-Object System.Collections.Generic.List[string]
    $sw = [Diagnostics.Stopwatch]::StartNew()
    try {
        $cached = $win.GetUpdatedCache($cr)
    } catch {
        return @{ error = $_.Exception.Message; ms = $sw.ElapsedMilliseconds }
    }
    $readMs = $sw.ElapsedMilliseconds
    $budget = $Budget
    $stack = New-Object System.Collections.Stack
    $stack.Push(@($cached, 0))
    while ($stack.Count -gt 0 -and $budget -gt 0) {
        $item = $stack.Pop(); $el = $item[0]; $d = $item[1]
        $budget--
        $stats.n++
        if ($d -gt $stats.depth) { $stats.depth = $d }
        $c = $el.Cached
        $role = $c.ControlType.ProgrammaticName -replace '^ControlType\.', ''
        $stats.roles[$role] = 1 + [int]$stats.roles[$role]
        $name = [string]$c.Name; $id = [string]$c.AutomationId; $cls = [string]$c.ClassName; $help = [string]$c.HelpText
        $label = ($name -replace '\s*\([^)]*\)\s*$', '').Trim().ToLowerInvariant()
        if ((($role -eq 'Button') -and ($label -match $callControl)) -or $id -eq 'hangup-button') { $stats.controls++ }
        if ("$name $help" -match $speakingRe) { $stats.speaking++ }
        if ("$name $id $cls" -match $captionRe) { $stats.captions++ }
        if ($role -in @('List', 'Tree', 'Table', 'DataGrid') -and $name.Trim() -match $rosterRe) { $stats.rosters++ }
        $kids = $el.CachedChildren
        if ($role -eq 'Document' -and ($null -eq $kids -or $kids.Count -eq 0)) { $stats.webEmpty++ }
        if ($Tree -and $treeLines.Count -lt 400) {
            $txt = @((Redact $name), (Redact $help)) | Where-Object { $_ -ne '' }
            $treeLines.Add(('  ' * [Math]::Min($d, 20)) + $role + $(if ($id) { "#$(Redact $id)" } else { '' }) +
                $(if ($cls) { " .$($cls.Split(' ')[0..2] -join '.')" } else { '' }) +
                $(if ($txt) { ' ' + (($txt | ForEach-Object { '"' + $_ + '"' }) -join ' ') } else { '' }))
        }
        if ($null -ne $kids) { for ($i = $kids.Count - 1; $i -ge 0; $i--) { $stack.Push(@($kids[$i], ($d + 1))) } }
    }
    return @{ stats = $stats; ms = $readMs; tree = $treeLines; truncated = ($budget -le 0) }
}

$pids = @(Get-Process -Name $Process -ErrorAction SilentlyContinue | ForEach-Object { $_.Id })
if ($pids.Count -eq 0) { Write-Error "no process named $Process"; exit 1 }
$root = [System.Windows.Automation.AutomationElement]::RootElement
$deadline = (Get-Date).AddSeconds($Seconds)
$round = 0
do {
    $round++
    $wins = @()
    foreach ($procId in $pids) {
        $cond = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ProcessIdProperty, $procId)
        $wins += @($root.FindAll([System.Windows.Automation.TreeScope]::Children, $cond))
    }
    Say "read $round at $(Get-Date -Format HH:mm:ss): $($wins.Count) top-level window(s) of $Process"
    foreach ($w in $wins) {
        $title = Redact ([string]$w.Current.Name)
        $r = Read-Window $w
        if ($r.error) { Say "  window `"$title`": read failed after $($r.ms) ms: $($r.error)"; continue }
        $s = $r.stats
        $top = ($s.roles.GetEnumerator() | Sort-Object Value -Descending | Select-Object -First 8 | ForEach-Object { "$($_.Key)=$($_.Value)" }) -join ' '
        Say ("  window `"$title`": $($s.n) elements$(if ($r.truncated) { ' (budget reached)' }) in $($r.ms) ms, depth $($s.depth); " +
            "call controls $($s.controls), 'speaking' labels $($s.speaking), elements named like captions $($s.captions) (also matches ordinary pages; look at -Tree), roster lists $($s.rosters), empty web documents $($s.webEmpty)")
        Say "    roles: $top"
        if ($Tree) { foreach ($l in $r.tree) { Say "    $l" } }
    }
    if ($Every -le 0) { break }
    Start-Sleep -Milliseconds ([int]($Every * 1000))
} while ((Get-Date) -lt $deadline)

if ($Out) { $lines | Out-File -FilePath $Out -Encoding utf8; Write-Output "written to $Out" }
