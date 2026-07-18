<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Card from '$lib/components/ui/card';
	import AnchorIcon from '@lucide/svelte/icons/anchor';

	let email = $state('');
	let password = $state('');
	let confirm = $state('');
	let err = $state('');
	let busy = $state(false);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (password !== confirm) {
			err = 'Passwords do not match';
			return;
		}
		busy = true;
		err = '';
		try {
			await api('/register', { method: 'POST', body: JSON.stringify({ email, password }) });
			toast.success('Welcome! Now enable 2FA in Settings — this panel is public-facing.', { duration: 10000 });
			goto('/settings');
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
			<Card.Description>Create your admin account — this runs once, on first setup.</Card.Description>
		</Card.Header>
		<Card.Content>
			<form onsubmit={submit} class="grid gap-4">
				<div class="grid gap-2">
					<Label for="email">Email</Label>
					<Input id="email" type="email" bind:value={email} required autocomplete="email" />
				</div>
				<div class="grid gap-2">
					<Label for="password">Password (min 8 chars)</Label>
					<Input id="password" type="password" bind:value={password} required minlength={8} autocomplete="new-password" />
				</div>
				<div class="grid gap-2">
					<Label for="confirm">Confirm password</Label>
					<Input id="confirm" type="password" bind:value={confirm} required autocomplete="new-password" />
				</div>
				{#if err}<p class="text-destructive text-sm">{err}</p>{/if}
				<Button type="submit" disabled={busy}>{busy ? 'Creating…' : 'Create account'}</Button>
			</form>
		</Card.Content>
	</Card.Root>
</div>
