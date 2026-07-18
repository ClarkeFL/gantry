<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { askConfirm } from '$lib/confirm.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Card from '$lib/components/ui/card';

	let githubUser = $state('');
	let githubTokenMasked = $state('');
	let qrBust = $state(0);

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
	});

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
			await api('/settings/totp/verify', { method: 'POST', body: JSON.stringify({ code: verifyCode }) });
			totpEnabled = true;
			totpPending = false;
			pendingSecret = '';
			toast.success('Two-factor authentication is on — codes required at every login');
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
			toast.success('GitHub settings saved — registry: ' + res.registry);
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
					: 'Strongly recommended — this panel is reachable from the internet.'}
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
				Email used for certificate registration and expiry notices — required before enabling HTTPS on any app.
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
			<Card.Title class="text-base">API tokens</Card.Title>
			<Card.Description>
				Give AI agents and scripts access to the panel API — create apps and databases, deploy,
				read logs, manage domains. Tokens can't change settings, your password or 2FA.
				Use as <code>Authorization: Bearer &lt;token&gt;</code>.
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-4">
			{#if freshToken}
				<div class="rounded-md border border-amber-500/40 bg-amber-500/10 p-3">
					<p class="mb-2 text-xs font-medium text-amber-500">
						Copy this token now — it won't be shown again.
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
							<span class="text-muted-foreground text-xs">created {t.created}</span>
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
</div>
