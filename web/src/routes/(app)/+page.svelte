<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Card from '$lib/components/ui/card';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import BoxIcon from '@lucide/svelte/icons/box';
	import DatabaseIcon from '@lucide/svelte/icons/database';

	type App = { name: string; running: boolean; category: string };
	type Service = { type: string; name: string; status: string };

	let apps = $state<App[]>([]);
	let services = $state<Service[]>([]);
	let loading = $state(true);

	const groups = $derived.by(() => {
		const g: Record<string, App[]> = {};
		for (const a of apps) (g[a.category || 'Uncategorised'] ??= []).push(a);
		return Object.entries(g).sort(([a], [b]) => a.localeCompare(b));
	});

	async function load() {
		loading = true;
		try {
			const d = await api('/apps');
			apps = d.apps ?? [];
			services = d.services ?? [];
		} finally {
			loading = false;
		}
	}
	onMount(load);
</script>

<div class="mx-auto max-w-5xl">
	<div class="mb-6 flex items-center justify-between">
		<h1 class="text-2xl font-semibold tracking-tight">Apps</h1>
		<Button variant="outline" size="sm" onclick={load} disabled={loading}>
			<RefreshCwIcon class="size-4 {loading ? 'animate-spin' : ''}" /> Refresh
		</Button>
	</div>

	{#if loading && !apps.length}
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
			{#each Array(3) as _, i (i)}<Skeleton class="h-28" />{/each}
		</div>
	{:else}
		{#each groups as [category, list] (category)}
			<h2 class="text-muted-foreground mt-6 mb-2 text-xs font-medium tracking-widest uppercase first:mt-0">
				{category}
			</h2>
			<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
				{#each list as app (app.name)}
					<a href="/app/{app.name}" class="group">
						<Card.Root class="group-hover:border-primary/50 h-full transition-colors">
							<Card.Header>
								<Card.Title class="flex items-center gap-2 text-base">
									<BoxIcon class="text-muted-foreground size-4" />
									{app.name}
									<span
										class="ml-auto size-2 rounded-full {app.running ? 'bg-emerald-500' : 'bg-red-500'}"
										title={app.running ? 'running' : 'stopped'}
									></span>
								</Card.Title>
								<Card.Description>{app.running ? 'Running' : 'Stopped'}</Card.Description>
							</Card.Header>
						</Card.Root>
					</a>
				{/each}
			</div>
		{/each}

		<h2 class="text-muted-foreground mt-10 mb-2 text-xs font-medium tracking-widest uppercase">Databases</h2>
		{#if services.length}
			<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
				{#each services as s (s.type + s.name)}
					<Card.Root>
						<Card.Header>
							<Card.Title class="flex items-center gap-2 text-base">
								<DatabaseIcon class="text-muted-foreground size-4" />
								{s.name}
								<Badge variant="secondary" class="ml-auto">{s.type}</Badge>
							</Card.Title>
							<Card.Description>{s.status}</Card.Description>
						</Card.Header>
					</Card.Root>
				{/each}
			</div>
		{:else}
			<p class="text-muted-foreground text-sm">
				No database services found. Install a dokku plugin (postgres, mysql, redis…) and create one.
			</p>
		{/if}
	{/if}
</div>
