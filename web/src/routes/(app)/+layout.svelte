<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { toast } from 'svelte-sonner';
	import AnchorIcon from '@lucide/svelte/icons/anchor';
	import LayoutGridIcon from '@lucide/svelte/icons/layout-grid';
	import ArchiveIcon from '@lucide/svelte/icons/archive';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import LogOutIcon from '@lucide/svelte/icons/log-out';

	let { children } = $props();
	let ready = $state(false);
	let version = $state('');
	let mock = $state(false);
	let updating = $state(false);
	let latest = $state('');
	let updateAvailable = $state(false);

	const nav = [
		{ href: '/', label: 'Apps', icon: LayoutGridIcon },
		{ href: '/backups', label: 'Backups', icon: ArchiveIcon }
	];

	onMount(async () => {
		try {
			const me = await api('/me');
			version = me.version;
			mock = me.mock;
			ready = true;
			const check = await api('/update/check');
			latest = check.latest;
			updateAvailable = check.available;
		} catch {
			// api() redirects to /login on 401
		}
	});

	async function logout() {
		await api('/logout', { method: 'POST' });
		location.href = '/login';
	}

	async function update() {
		if (!confirm(`Download ${latest || 'the latest version'} and restart the panel?`)) return;
		updating = true;
		try {
			await api('/update', { method: 'POST' });
			toast.success('Updating — the panel restarts in a few seconds');
			setTimeout(() => location.reload(), 5000);
		} catch (e) {
			toast.error(e instanceof Error ? e.message : String(e));
			updating = false;
		}
	}
</script>

{#if ready}
	<div class="min-h-svh">
		<aside class="bg-card fixed inset-y-0 flex w-56 flex-col border-r">
			<div class="flex items-center gap-2 px-4 py-5">
				<AnchorIcon class="text-primary size-5" />
				<span class="text-lg font-semibold tracking-tight">gantry</span>
				{#if mock}
					<span class="ml-auto rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-500">
						mock
					</span>
				{/if}
			</div>
			<nav class="grid gap-1 px-2">
				{#each nav as item (item.href)}
					<a
						href={item.href}
						class="flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-colors
						{page.url.pathname === item.href
							? 'bg-accent text-accent-foreground'
							: 'text-muted-foreground hover:bg-accent/50 hover:text-foreground'}"
					>
						<item.icon class="size-4" />
						{item.label}
					</a>
				{/each}
			</nav>
			<div class="mt-auto grid gap-1 p-2">
				{#if updateAvailable}
					<Button
						variant="ghost"
						size="sm"
						class="justify-start gap-2 text-amber-500 hover:text-amber-400"
						onclick={update}
						disabled={updating}
					>
						<span class="relative flex size-4 items-center justify-center">
							<DownloadIcon class="size-4" />
							<span class="absolute -top-0.5 -right-0.5 size-2 animate-pulse rounded-full bg-amber-500"></span>
						</span>
						{updating ? 'Updating…' : `Update to ${latest}`}
					</Button>
				{:else}
					<Button variant="ghost" size="sm" class="justify-start gap-2" onclick={update} disabled={updating}>
						<DownloadIcon class="size-4" />
						{updating ? 'Updating…' : 'Update panel'}
					</Button>
				{/if}
				<Button variant="ghost" size="sm" class="justify-start gap-2" onclick={logout}>
					<LogOutIcon class="size-4" /> Log out
				</Button>
				<p class="text-muted-foreground px-3 py-1 text-xs">{version}</p>
			</div>
		</aside>
		<main class="ml-56 p-6 lg:p-8">
			{@render children()}
		</main>
	</div>
{/if}
