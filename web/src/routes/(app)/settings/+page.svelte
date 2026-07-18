<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { toast } from 'svelte-sonner';
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

	// docker registries
	type Registry = { server: string; user: string };
	let registries = $state<Registry[]>([]);
	let regServer = $state('');
	let regUser = $state('');
	let regPassword = $state('');
	let savingReg = $state(false);

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
		registries = s.registries ?? [];
	});

	async function addRegistry(e: SubmitEvent) {
		e.preventDefault();
		savingReg = true;
		try {
			const res = await api('/settings/registry', {
				method: 'POST',
				body: JSON.stringify({ server: regServer.trim(), user: regUser.trim(), password: regPassword })
			});
			registries = res.registries;
			regPassword = '';
			toast.success('Registry login succeeded');
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingReg = false;
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
		if (!confirm('Disable 2FA? Login falls back to email + password only.')) return;
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
			<Card.Title class="text-base">Docker registries</Card.Title>
			<Card.Description>
				Log the server in to pull private images (Docker Hub, GitLab, …). Passwords go straight to
				<code>docker login</code> and are not stored by the panel. ghcr.io is covered by the GitHub card below.
			</Card.Description>
		</Card.Header>
		<Card.Content class="grid gap-4">
			{#if registries.length}
				<div class="flex flex-wrap gap-2">
					{#each registries as reg (reg.server)}
						<code class="bg-muted rounded px-2 py-1 text-xs">{reg.user} @ {reg.server}</code>
					{/each}
				</div>
			{/if}
			<form onsubmit={addRegistry} class="grid max-w-sm gap-3">
				<div class="grid gap-1">
					<Label for="reg-server">Registry</Label>
					<Input id="reg-server" bind:value={regServer} placeholder="docker.io (default)" class="font-mono text-xs" />
				</div>
				<div class="grid grid-cols-2 gap-3">
					<div class="grid gap-1">
						<Label for="reg-user">Username</Label>
						<Input id="reg-user" bind:value={regUser} required />
					</div>
					<div class="grid gap-1">
						<Label for="reg-pass">Password / token</Label>
						<Input id="reg-pass" type="password" bind:value={regPassword} required />
					</div>
				</div>
				<Button type="submit" class="w-fit" disabled={savingReg}>
					{savingReg ? 'Logging in…' : 'Log in to registry'}
				</Button>
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
