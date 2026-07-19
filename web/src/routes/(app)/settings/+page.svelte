<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { askConfirm } from '$lib/confirm.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import * as Card from '$lib/components/ui/card';
	import { fmtDate, fmtLogLine } from '$lib/dates';
	import {
		displayPrefs,
		browserTzName,
		serverInfo,
		serverTzLabel,
		userTzLabel,
		userTzFull,
		userTzName
	} from '$lib/server-info.svelte';
	import InfoTip from '$lib/components/info-tip.svelte';

	// Common IANA zones for the schedule UI. "Auto" uses the browser zone.
	const TIMEZONES: { value: string; label: string }[] = [
		{ value: '', label: 'Auto (browser)' },
		{ value: 'UTC', label: 'UTC' },
		{ value: 'Pacific/Honolulu', label: 'Hawaii (HST)' },
		{ value: 'America/Los_Angeles', label: 'US Pacific' },
		{ value: 'America/Denver', label: 'US Mountain' },
		{ value: 'America/Chicago', label: 'US Central' },
		{ value: 'America/New_York', label: 'US Eastern' },
		{ value: 'America/Sao_Paulo', label: 'São Paulo' },
		{ value: 'Europe/London', label: 'London' },
		{ value: 'Europe/Paris', label: 'Paris / Berlin' },
		{ value: 'Europe/Athens', label: 'Athens / Eastern Europe' },
		{ value: 'Africa/Johannesburg', label: 'Johannesburg' },
		{ value: 'Asia/Dubai', label: 'Dubai' },
		{ value: 'Asia/Kolkata', label: 'India' },
		{ value: 'Asia/Singapore', label: 'Singapore' },
		{ value: 'Asia/Tokyo', label: 'Tokyo' },
		{ value: 'Australia/Perth', label: 'Perth (AWST)' },
		{ value: 'Australia/Adelaide', label: 'Adelaide (ACST/ACDT)' },
		{ value: 'Australia/Sydney', label: 'Sydney / Melbourne (AEST/AEDT)' },
		{ value: 'Pacific/Auckland', label: 'Auckland' }
	];

	let githubUser = $state('');
	let githubTokenMasked = $state('');
	let qrBust = $state(0);

	// timezone preference
	let displayTz = $state('');
	let savingTz = $state(false);

	// password change
	let curPw = $state('');
	let newPw = $state('');
	let confirmPw = $state('');
	let savingPw = $state(false);

	// 2FA
	let totpEnabled = $state(false);
	let totpPending = $state(false);
	let pendingSecret = $state('');
	let verifyCode = $state('');
	let disablePw = $state(false);
	let disablePassword = $state('');
	let totpBusy = $state(false);
	let recoveryCodes = $state<string[]>([]);
	let recoveryLeft = $state(0);

	// alert webhook
	let alertWebhook = $state('');
	let alertsEnabled = $state(false);
	let savingWebhook = $state(false);

	// audit log
	let auditLines = $state<string[]>([]);

	async function saveWebhook(e?: SubmitEvent) {
		e?.preventDefault();
		savingWebhook = true;
		try {
			await api('/settings/webhook', {
				method: 'POST',
				body: JSON.stringify({ url: alertWebhook.trim(), enabled: alertsEnabled })
			});
			toast.success(alertsEnabled && alertWebhook.trim() ? 'Alerts on' : 'Saved. Alerts are off');
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingWebhook = false;
		}
	}

	function toggleAlerts(on: boolean) {
		alertsEnabled = on;
		saveWebhook();
	}

	async function loadAudit() {
		const d = await api('/audit');
		auditLines = d.lines ?? [];
	}

	// github
	let ghUser = $state('');
	let ghToken = $state('');
	let savingGh = $state(false);

	// letsencrypt
	let leEmail = $state('');
	let savingLe = $state(false);

	// session length
	let sessionDays = $state(7);
	let savingSession = $state(false);

	// API tokens
	let tokens = $state<{ name: string; created: string }[]>([]);
	let newTokenName = $state('');
	let freshToken = $state('');
	let creatingToken = $state(false);

	async function createToken(e: SubmitEvent) {
		e.preventDefault();
		creatingToken = true;
		try {
			const res = await api('/settings/tokens', {
				method: 'POST',
				body: JSON.stringify({ name: newTokenName.trim() })
			});
			freshToken = res.token;
			newTokenName = '';
			const s = await api('/settings');
			tokens = s.tokens ?? [];
		} catch (e) {
			toast.error(msg(e));
		} finally {
			creatingToken = false;
		}
	}

	async function revokeToken(name: string) {
		if (!(await askConfirm(`Revoke token "${name}"? Anything using it loses access immediately.`))) return;
		try {
			await api('/settings/tokens', { method: 'DELETE', body: JSON.stringify({ name }) });
			tokens = tokens.filter((t) => t.name !== name);
			toast.success(`Revoked "${name}"`);
		} catch (e) {
			toast.error(msg(e));
		}
	}

	function copyToken() {
		navigator.clipboard.writeText(freshToken);
		toast.success('Token copied');
	}


	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	onMount(async () => {
		const s = await api('/settings');
		githubUser = s.githubUser;
		githubTokenMasked = s.githubToken;
		ghUser = s.githubUser;
		leEmail = s.leEmail;
		totpEnabled = s.totpEnabled;
		totpPending = s.totpPending;
		pendingSecret = s.pendingSecret ?? '';
		sessionDays = s.sessionDays ?? 7;
		tokens = s.tokens ?? [];
		recoveryLeft = s.recoveryLeft ?? 0;
		alertWebhook = s.alertWebhook ?? '';
		alertsEnabled = s.alertsEnabled ?? false;
		displayTz = s.displayTz ?? '';
		displayPrefs.tz = displayTz;
		loadAudit().catch(() => {});
	});

	async function saveTimezone(e: SubmitEvent) {
		e.preventDefault();
		savingTz = true;
		try {
			await api('/settings/timezone', {
				method: 'POST',
				body: JSON.stringify({ tz: displayTz })
			});
			displayPrefs.tz = displayTz;
			toast.success(
				displayTz
					? `Schedules will use ${userTzFull()}`
					: `Schedules will use your browser timezone (${userTzFull()})`
			);
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingTz = false;
		}
	}

	async function saveSessionDays(e: SubmitEvent) {
		e.preventDefault();
		savingSession = true;
		try {
			await api('/settings/session', { method: 'POST', body: JSON.stringify({ days: sessionDays }) });
			toast.success(`Logins now last ${sessionDays} days`);
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingSession = false;
		}
	}

	async function startTotpSetup() {
		totpBusy = true;
		try {
			const res = await api('/settings/totp/setup', { method: 'POST' });
			pendingSecret = res.secret;
			totpPending = true;
			verifyCode = '';
			qrBust = Date.now();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			totpBusy = false;
		}
	}

	async function verifyTotp(e: SubmitEvent) {
		e.preventDefault();
		totpBusy = true;
		try {
			const res = await api('/settings/totp/verify', { method: 'POST', body: JSON.stringify({ code: verifyCode }) });
			totpEnabled = true;
			totpPending = false;
			pendingSecret = '';
			recoveryCodes = res.recovery ?? [];
			recoveryLeft = recoveryCodes.length;
			toast.success('Two-factor authentication is on, codes required at every login');
		} catch (e) {
			toast.error(msg(e));
		} finally {
			totpBusy = false;
		}
	}

	async function disableTotp(e: SubmitEvent) {
		e.preventDefault();
		if (!(await askConfirm('Disable 2FA? Login falls back to email + password only.'))) return;
		totpBusy = true;
		try {
			await api('/settings/totp/disable', { method: 'POST', body: JSON.stringify({ password: disablePassword }) });
			totpEnabled = false;
			disablePw = false;
			disablePassword = '';
			toast.success('Two-factor authentication disabled');
		} catch (e) {
			toast.error(msg(e));
		} finally {
			totpBusy = false;
		}
	}

	async function saveLeEmail(e: SubmitEvent) {
		e.preventDefault();
		savingLe = true;
		try {
			await api('/settings/letsencrypt', { method: 'POST', body: JSON.stringify({ email: leEmail.trim() }) });
			toast.success("Let's Encrypt email saved");
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingLe = false;
		}
	}

	async function changePassword(e: SubmitEvent) {
		e.preventDefault();
		if (newPw !== confirmPw) {
			toast.error('New passwords do not match');
			return;
		}
		savingPw = true;
		try {
			await api('/settings/password', {
				method: 'POST',
				body: JSON.stringify({ current: curPw, new: newPw })
			});
			toast.success('Password changed');
			curPw = newPw = confirmPw = '';
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingPw = false;
		}
	}

	async function saveGitHub(e: SubmitEvent) {
		e.preventDefault();
		savingGh = true;
		try {
			const res = await api('/settings/github', {
				method: 'POST',
				body: JSON.stringify({ user: ghUser.trim(), token: ghToken.trim() })
			});
			toast.success('GitHub settings saved, registry: ' + res.registry);
			ghToken = '';
			const s = await api('/settings');
			githubTokenMasked = s.githubToken;
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingGh = false;
		}
	}
</script>

<div class="mx-auto grid max-w-3xl gap-6">
	<h1 class="text-2xl font-semibold tracking-tight">Settings</h1>

	<Card.Root>
		<Card.Header>
			<Card.Title class="flex items-center gap-2 text-base">
				Two-factor authentication
				{#if totpEnabled}
					<span class="rounded bg-emerald-500/15 px-1.5 py-0.5 text-xs font-medium text-emerald-500">on</span>
				{:else}
					<span class="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs font-medium text-amber-500">off</span>
				{/if}
			</Card.Title>
			<Card.Description>
				{totpEnabled
					? 'A code from your authenticator app is required at every login.'
					: 'Strongly recommended, this panel is reachable from the internet.'}
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-4">
			{#if totpPending}
				<div class="flex flex-wrap items-start gap-6">
					<img
						src="/api/settings/totp.png?v={qrBust}"
						alt="TOTP QR code"
						width="180"
						height="180"
						class="rounded-lg border bg-white p-2"
					/>
					<div class="grid min-w-64 flex-1 gap-3">
						<p class="text-muted-foreground text-sm">
							1. Scan with Google Authenticator, 1Password, Authy…<br />
							2. Enter the 6-digit code it shows to confirm.
						</p>
						<div class="grid gap-1">
							<Label>Secret (manual entry)</Label>
							<code class="bg-muted rounded px-2 py-1.5 text-xs break-all">{pendingSecret}</code>
						</div>
						<form onsubmit={verifyTotp} class="flex gap-2">
							<Input
								inputmode="numeric"
								maxlength={6}
								placeholder="123456"
								bind:value={verifyCode}
								required
								class="w-32 font-mono tracking-widest"
							/>
							<Button type="submit" disabled={totpBusy}>Confirm & enable</Button>
						</form>
					</div>
				</div>
			{:else if totpEnabled}
				{#if recoveryCodes.length}
					<div class="rounded-md border border-amber-500/40 bg-amber-500/10 p-3">
						<p class="mb-2 text-xs font-medium text-amber-500">
							Recovery codes, save these somewhere safe now, they won't be shown again. Each works
							once at login if you lose your authenticator.
						</p>
						<div class="grid grid-cols-4 gap-1 font-mono text-xs">
							{#each recoveryCodes as c (c)}<code class="bg-muted rounded px-1.5 py-1">{c}</code>{/each}
						</div>
						<Button
							size="sm"
							variant="outline"
							class="mt-2"
							onclick={() => {
								navigator.clipboard.writeText(recoveryCodes.join('\n'));
								toast.success('Copied');
							}}>Copy all</Button
						>
						<Button size="sm" variant="ghost" class="mt-2" onclick={() => (recoveryCodes = [])}>Done</Button>
					</div>
				{:else}
					<p class="text-muted-foreground text-xs">{recoveryLeft} recovery codes remaining.</p>
				{/if}
				{#if disablePw}
					<form onsubmit={disableTotp} class="flex max-w-sm gap-2">
						<Input type="password" bind:value={disablePassword} required placeholder="Current password" />
						<Button type="submit" variant="destructive" disabled={totpBusy}>Disable</Button>
					</form>
				{:else}
					<div class="flex gap-2">
						<Button variant="outline" onclick={startTotpSetup} disabled={totpBusy}>Regenerate secret</Button>
						<Button variant="ghost" class="text-destructive" onclick={() => (disablePw = true)}>Disable 2FA</Button>
					</div>
				{/if}
			{:else}
				<Button class="w-fit" onclick={startTotpSetup} disabled={totpBusy}>Enable two-factor authentication</Button>
			{/if}
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="flex items-center gap-1.5 text-base">
				Your timezone
				<InfoTip
					text={`Backup and job schedules run on the server clock. Set your timezone so time pickers mean your local time, and we convert to the server automatically. Server is currently ${serverTzLabel() || 'unknown'}.`}
				/>
			</Card.Title>
			<Card.Description>
				Used for every schedule picker and timestamp across the panel. Cron still runs on the
				server{#if serverInfo.known}
					{' '}({serverTzLabel()}).
				{:else}
					.
				{/if}
			</Card.Description>
		</Card.Header>
		<Card.Content>
			<form onsubmit={saveTimezone} class="flex max-w-md flex-wrap items-center gap-2">
				<select
					class="border-input dark:bg-input/30 h-9 min-w-56 flex-1 rounded-md border bg-transparent px-2.5 text-sm shadow-xs"
					bind:value={displayTz}
					aria-label="Your timezone"
				>
					{#each TIMEZONES as z (z.value || 'auto')}
						<option value={z.value}>
							{z.label}{z.value === '' ? ` · ${browserTzName()}` : ''}
						</option>
					{/each}
				</select>
				<Button type="submit" variant="outline" disabled={savingTz}>
					{savingTz ? 'Saving…' : 'Save'}
				</Button>
			</form>
			<p class="text-muted-foreground mt-2 text-xs">
				Active: <span class="text-foreground font-medium">{userTzFull()}</span>
				{#if !displayTz}
					<span class="opacity-70">(from this browser · {userTzName()})</span>
				{/if}
			</p>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">Session length</Card.Title>
			<Card.Description>
				How long a login stays valid (1–90 days). Sessions survive panel restarts and updates.
			</Card.Description>
		</Card.Header>
		<Card.Content>
			<form onsubmit={saveSessionDays} class="flex max-w-xs items-center gap-2">
				<Input type="number" min={1} max={90} bind:value={sessionDays} class="w-24" required />
				<span class="text-muted-foreground text-sm">days</span>
				<Button type="submit" variant="outline" disabled={savingSession}>Save</Button>
			</form>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">Change password</Card.Title>
		</Card.Header>
		<Card.Content>
			<form onsubmit={changePassword} class="grid max-w-sm gap-3">
				<div class="grid gap-1">
					<Label for="cur-pw">Current password</Label>
					<Input id="cur-pw" type="password" bind:value={curPw} required autocomplete="current-password" />
				</div>
				<div class="grid gap-1">
					<Label for="new-pw">New password (min 8 chars)</Label>
					<Input id="new-pw" type="password" bind:value={newPw} required minlength={8} autocomplete="new-password" />
				</div>
				<div class="grid gap-1">
					<Label for="confirm-pw">Confirm new password</Label>
					<Input id="confirm-pw" type="password" bind:value={confirmPw} required autocomplete="new-password" />
				</div>
				<Button type="submit" class="mt-1 w-fit" disabled={savingPw}>
					{savingPw ? 'Saving…' : 'Change password'}
				</Button>
			</form>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">Let's Encrypt</Card.Title>
			<Card.Description>
				Email used for certificate registration and expiry notices, required before enabling HTTPS on any app.
			</Card.Description>
		</Card.Header>
		<Card.Content>
			<form onsubmit={saveLeEmail} class="flex max-w-sm gap-2">
				<Input type="email" bind:value={leEmail} required placeholder="you@example.com" />
				<Button type="submit" variant="outline" disabled={savingLe}>Save</Button>
			</form>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="flex items-center gap-2 text-base">
				Alerts
				<div class="ml-auto flex items-center gap-2">
					<span class="text-xs {alertsEnabled ? 'text-emerald-500' : 'text-muted-foreground'}">
						{alertsEnabled ? 'On' : 'Off'}
					</span>
					<Switch
						checked={alertsEnabled}
						onCheckedChange={toggleAlerts}
						disabled={savingWebhook || !alertWebhook.trim()}
						aria-label="Alerts on or off"
					/>
				</div>
			</Card.Title>
			<Card.Description>
				A Slack or Discord webhook URL, gantry posts there when a deploy or backup fails. The
				switch turns alerts off without losing the URL.
			</Card.Description>
		</Card.Header>
		<Card.Content>
			<form onsubmit={saveWebhook} class="flex max-w-lg gap-2">
				<Input bind:value={alertWebhook} type="url" placeholder="https://hooks.slack.com/services/… or https://discord.com/api/webhooks/…" />
				<Button type="submit" variant="outline" disabled={savingWebhook}>Save</Button>
			</form>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">API tokens</Card.Title>
			<Card.Description>
				Give AI agents and scripts access to the panel API, create apps and databases, deploy,
				read logs, manage domains. Tokens can't change settings, your password or 2FA.
				Use as <code>Authorization: Bearer &lt;token&gt;</code>.
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-4">
			{#if freshToken}
				<div class="rounded-md border border-amber-500/40 bg-amber-500/10 p-3">
					<p class="mb-2 text-xs font-medium text-amber-500">
						Copy this token now, it won't be shown again.
					</p>
					<div class="flex items-center gap-2">
						<code class="bg-muted flex-1 rounded px-2 py-1.5 font-mono text-xs break-all">{freshToken}</code>
						<Button size="sm" variant="outline" onclick={copyToken}>Copy</Button>
						<Button size="sm" variant="ghost" onclick={() => (freshToken = '')}>Done</Button>
					</div>
				</div>
			{/if}
			{#if tokens.length}
				<div class="grid gap-2">
					{#each tokens as t (t.name)}
						<div class="flex items-center gap-3 rounded-md border px-3 py-2 text-sm">
							<span class="font-medium">{t.name}</span>
							<span class="text-muted-foreground text-xs">created {fmtDate(t.created)}</span>
							<Button size="sm" variant="ghost" class="text-destructive ml-auto" onclick={() => revokeToken(t.name)}>
								Revoke
							</Button>
						</div>
					{/each}
				</div>
			{/if}
			<form onsubmit={createToken} class="flex max-w-sm gap-2">
				<Input bind:value={newTokenName} required placeholder="e.g. claude-agent" />
				<Button type="submit" variant="outline" disabled={creatingToken}>Create token</Button>
			</form>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">GitHub</Card.Title>
			<Card.Description>
				A personal access token lets the panel pull private images from ghcr.io
				(<code>dokku registry:login</code>) and raises the rate limit on update checks.
				{#if githubTokenMasked}<br />Current token: <code>{githubTokenMasked}</code>{/if}
			</Card.Description>
		</Card.Header>
		<Card.Content>
			<form onsubmit={saveGitHub} class="grid max-w-sm gap-3">
				<div class="grid gap-1">
					<Label for="gh-user">GitHub username</Label>
					<Input id="gh-user" bind:value={ghUser} placeholder="ClarkeFL" />
				</div>
				<div class="grid gap-1">
					<Label for="gh-token">Token</Label>
					<Input id="gh-token" type="password" bind:value={ghToken} placeholder="ghp_… or github_pat_…" />
				</div>
				<Button type="submit" class="mt-1 w-fit" disabled={savingGh}>
					{savingGh ? 'Saving…' : 'Save'}
				</Button>
			</form>
		</Card.Content>
	</Card.Root>

	<Card.Root>
		<Card.Header>
			<Card.Title class="text-base">Audit log</Card.Title>
			<Card.Description>
				Every state-changing action, newest first, who (admin session or API token), from where,
				and what. <button class="underline" onclick={loadAudit}>Refresh</button>
			</Card.Description>
		</Card.Header>
		<Card.Content>
			{#if auditLines.length}
				<div class="bg-card max-h-64 overflow-y-auto rounded-md border p-3 font-mono text-xs leading-5">
					{#each auditLines as line, i (i)}<div class="whitespace-pre">{fmtLogLine(line)}</div>{/each}
				</div>
			{:else}
				<p class="text-muted-foreground text-sm">Nothing yet.</p>
			{/if}
		</Card.Content>
	</Card.Root>
</div>
