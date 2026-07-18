<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Card from '$lib/components/ui/card';

	let totpSecret = $state('');
	let githubUser = $state('');
	let githubTokenMasked = $state('');
	let qrBust = $state(0);

	// password change
	let curPw = $state('');
	let newPw = $state('');
	let confirmPw = $state('');
	let savingPw = $state(false);

	// totp regen
	let regenPw = $state('');
	let regening = $state(false);

	// github
	let ghUser = $state('');
	let ghToken = $state('');
	let savingGh = $state(false);

	// letsencrypt
	let leEmail = $state('');
	let savingLe = $state(false);

	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	onMount(async () => {
		const s = await api('/settings');
		totpSecret = s.totpSecret;
		githubUser = s.githubUser;
		githubTokenMasked = s.githubToken;
		ghUser = s.githubUser;
		leEmail = s.leEmail;
	});

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

	async function regenTotp(e: SubmitEvent) {
		e.preventDefault();
		if (!confirm('Old 2FA codes stop working immediately. Continue?')) return;
		regening = true;
		try {
			const res = await api('/settings/totp', {
				method: 'POST',
				body: JSON.stringify({ password: regenPw })
			});
			totpSecret = res.secret;
			qrBust = Date.now();
			regenPw = '';
			toast.success('New 2FA secret generated — scan the new QR code now');
		} catch (e) {
			toast.error(msg(e));
		} finally {
			regening = false;
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
			<Card.Title class="text-base">Two-factor authentication</Card.Title>
			<Card.Description>Scan the QR code with Google Authenticator, 1Password, Authy…</Card.Description>
		</Card.Header>
		<Card.Content class="flex flex-wrap items-start gap-6">
			<img
				src="/api/settings/totp.png?v={qrBust}"
				alt="TOTP QR code"
				width="180"
				height="180"
				class="rounded-lg border bg-white p-2"
			/>
			<div class="grid min-w-64 flex-1 gap-4">
				<div class="grid gap-1">
					<Label>Secret (manual entry)</Label>
					<code class="bg-muted rounded px-2 py-1.5 text-xs break-all">{totpSecret}</code>
				</div>
				<form onsubmit={regenTotp} class="grid gap-2">
					<Label for="regen-pw">Regenerate secret (enter password to confirm)</Label>
					<div class="flex gap-2">
						<Input id="regen-pw" type="password" bind:value={regenPw} required placeholder="Current password" />
						<Button type="submit" variant="outline" disabled={regening}>Regenerate</Button>
					</div>
				</form>
			</div>
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
