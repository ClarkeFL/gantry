<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Card from '$lib/components/ui/card';
	import AnchorIcon from '@lucide/svelte/icons/anchor';

	let password = $state('');
	let code = $state('');
	let err = $state('');
	let busy = $state(false);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		err = '';
		try {
			await api('/login', { method: 'POST', body: JSON.stringify({ password, code }) });
			goto('/');
		} catch (e) {
			err = e instanceof Error ? e.message : String(e);
		} finally {
			busy = false;
		}
	}
</script>

<div class="flex min-h-svh items-center justify-center p-4">
	<Card.Root class="w-full max-w-sm">
		<Card.Header>
			<Card.Title class="flex items-center gap-2 text-2xl">
				<AnchorIcon class="text-primary size-6" /> gantry
			</Card.Title>
			<Card.Description>Sign in to your server panel</Card.Description>
		</Card.Header>
		<Card.Content>
			<form onsubmit={submit} class="grid gap-4">
				<div class="grid gap-2">
					<Label for="password">Password</Label>
					<Input id="password" type="password" bind:value={password} required autocomplete="current-password" />
				</div>
				<div class="grid gap-2">
					<Label for="code">2FA code</Label>
					<Input
						id="code"
						inputmode="numeric"
						placeholder="123456"
						bind:value={code}
						required
						maxlength={6}
						class="font-mono tracking-widest"
					/>
				</div>
				{#if err}<p class="text-destructive text-sm">{err}</p>{/if}
				<Button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</Button>
			</form>
		</Card.Content>
	</Card.Root>
</div>
