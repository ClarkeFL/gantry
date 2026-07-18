<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Card from '$lib/components/ui/card';
	import AnchorIcon from '@lucide/svelte/icons/anchor';
	import ShieldCheckIcon from '@lucide/svelte/icons/shield-check';

	let email = $state('');
	let password = $state('');
	let code = $state('');
	let mfaToken = $state('');
	let step = $state<'credentials' | 'mfa'>('credentials');
	let err = $state('');
	let busy = $state(false);

	onMount(async () => {
		// first boot → no account yet → go register instead
		const res = await fetch('/api/me');
		if (res.ok && (await res.json()).setup) goto('/register');
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		err = '';
		try {
			const res = await api('/login', { method: 'POST', body: JSON.stringify({ email, password }) });
			if (res.mfa) {
				mfaToken = res.token;
				step = 'mfa';
				code = '';
			} else {
				goto('/');
			}
		} catch (e) {
			err = e instanceof Error ? e.message : String(e);
		} finally {
			busy = false;
		}
	}

	async function submitCode(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		err = '';
		try {
			await api('/login/mfa', { method: 'POST', body: JSON.stringify({ token: mfaToken, code }) });
			goto('/');
		} catch (e) {
			err = e instanceof Error ? e.message : String(e);
			if (String(err).includes('expired')) step = 'credentials';
		} finally {
			busy = false;
		}
	}
</script>

<div class="flex min-h-svh items-center justify-center p-4">
	<Card.Root class="w-full max-w-sm">
		{#if step === 'credentials'}
			<Card.Header>
				<Card.Title class="flex items-center gap-2 text-2xl">
					<AnchorIcon class="text-primary size-6" /> gantry
				</Card.Title>
				<Card.Description>Sign in to your server panel</Card.Description>
			</Card.Header>
			<Card.Content>
				<form onsubmit={submit} class="grid gap-4">
					<div class="grid gap-2">
						<Label for="email">Email</Label>
						<Input id="email" type="email" bind:value={email} required autocomplete="email" />
					</div>
					<div class="grid gap-2">
						<Label for="password">Password</Label>
						<Input id="password" type="password" bind:value={password} required autocomplete="current-password" />
					</div>
					{#if err}<p class="text-destructive text-sm">{err}</p>{/if}
					<Button type="submit" disabled={busy}>{busy ? 'Checking…' : 'Continue'}</Button>
				</form>
			</Card.Content>
		{:else}
			<Card.Header>
				<Card.Title class="flex items-center gap-2 text-xl">
					<ShieldCheckIcon class="text-primary size-5" /> Two-factor code
				</Card.Title>
				<Card.Description>Enter the 6-digit code from your authenticator app.</Card.Description>
			</Card.Header>
			<Card.Content>
				<form onsubmit={submitCode} class="grid gap-4">
					<!-- svelte-ignore a11y_autofocus -->
					<Input
						inputmode="numeric"
						placeholder="123456"
						bind:value={code}
						required
						maxlength={6}
						autofocus
						class="text-center font-mono text-lg tracking-[0.5em]"
					/>
					{#if err}<p class="text-destructive text-sm">{err}</p>{/if}
					<Button type="submit" disabled={busy}>{busy ? 'Verifying…' : 'Verify & sign in'}</Button>
					<button
						type="button"
						class="text-muted-foreground hover:text-foreground text-sm"
						onclick={() => (step = 'credentials')}
					>
						Back
					</button>
				</form>
			</Card.Content>
		{/if}
	</Card.Root>
</div>
