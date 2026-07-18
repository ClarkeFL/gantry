<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, stream } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Card from '$lib/components/ui/card';
	import * as Dialog from '$lib/components/ui/dialog';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import BoxIcon from '@lucide/svelte/icons/box';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import PlusIcon from '@lucide/svelte/icons/plus';

	type App = { name: string; running: boolean; category: string };
	type Service = { type: string; name: string; status: string };

	let apps = $state<App[]>([]);
	let services = $state<Service[]>([]);
	let loading = $state(true);

	// new app dialog
	let newAppOpen = $state(false);
	let newAppName = $state('');
	let newAppCategory = $state('');
	let creatingApp = $state(false);

	// new database dialog
	let newDbOpen = $state(false);
	let newDbType = $state('postgres');
	let newDbName = $state('');
	let creatingDb = $state(false);
	let dbLines = $state<string[]>([]);

	const groups = $derived.by(() => {
		const g: Record<string, App[]> = {};
		for (const a of apps) (g[a.category || 'Uncategorised'] ??= []).push(a);
		return Object.entries(g).sort(([a], [b]) => a.localeCompare(b));
	});
	const categories = $derived([...new Set(apps.map((a) => a.category).filter(Boolean))].sort());

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

	async function createApp(e: SubmitEvent) {
		e.preventDefault();
		creatingApp = true;
		try {
			await api('/apps', {
				method: 'POST',
				body: JSON.stringify({ name: newAppName.trim(), category: newAppCategory.trim() })
			});
			toast.success(`Created ${newAppName}`);
			newAppOpen = false;
			goto('/app/' + newAppName.trim());
		} catch (e) {
			toast.error(e instanceof Error ? e.message : String(e));
		} finally {
			creatingApp = false;
		}
	}

	function createDb(e: SubmitEvent) {
		e.preventDefault();
		creatingDb = true;
		dbLines = [];
		stream(
			'/services',
			(l) => {
				dbLines.push(l);
			},
			{ method: 'POST', body: JSON.stringify({ type: newDbType, name: newDbName.trim() }) },
			() => {
				creatingDb = false;
				load();
			}
		);
	}
</script>

<div class="mx-auto max-w-5xl">
	<div class="mb-6 flex flex-wrap items-center gap-2">
		<h1 class="text-2xl font-semibold tracking-tight">Apps</h1>
		<div class="ml-auto flex gap-2">
			<Button variant="outline" size="sm" onclick={load} disabled={loading}>
				<RefreshCwIcon class="size-4 {loading ? 'animate-spin' : ''}" /> Refresh
			</Button>
			<Button variant="outline" size="sm" onclick={() => (newDbOpen = true)}>
				<DatabaseIcon class="size-4" /> New database
			</Button>
			<Button size="sm" onclick={() => (newAppOpen = true)}>
				<PlusIcon class="size-4" /> New app
			</Button>
		</div>
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
		{:else}
			<p class="text-muted-foreground text-sm">No apps yet — create one to get started.</p>
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
				No database services yet — create one with the button above (needs the matching dokku plugin installed).
			</p>
		{/if}
	{/if}
</div>

<Dialog.Root bind:open={newAppOpen}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>New app</Dialog.Title>
			<Dialog.Description>
				Creates an empty dokku app — deploy into it from its page afterwards.
			</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={createApp} class="grid gap-4">
			<div class="grid gap-2">
				<Label for="app-name">Name</Label>
				<Input id="app-name" bind:value={newAppName} placeholder="my-api" required pattern="[a-z0-9][a-z0-9.-]*" />
			</div>
			<div class="grid gap-2">
				<Label for="app-cat">Category <span class="text-muted-foreground">(new or existing)</span></Label>
				<Input id="app-cat" bind:value={newAppCategory} placeholder="e.g. Websites" list="cats" />
				<datalist id="cats">
					{#each categories as c (c)}<option value={c}></option>{/each}
				</datalist>
			</div>
			<Button type="submit" disabled={creatingApp}>{creatingApp ? 'Creating…' : 'Create app'}</Button>
		</form>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={newDbOpen}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>New database</Dialog.Title>
			<Dialog.Description>
				Runs <code>dokku &lt;type&gt;:create</code> — the matching plugin must be installed on the server.
			</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={createDb} class="grid gap-4">
			<div class="grid gap-2">
				<Label for="db-type">Type</Label>
				<select
					id="db-type"
					bind:value={newDbType}
					class="border-input bg-transparent dark:bg-input/30 h-9 rounded-md border px-3 text-sm shadow-xs"
				>
					<option value="postgres">postgres</option>
					<option value="mysql">mysql</option>
					<option value="mariadb">mariadb</option>
					<option value="redis">redis</option>
					<option value="mongo">mongo</option>
				</select>
			</div>
			<div class="grid gap-2">
				<Label for="db-name">Name</Label>
				<Input id="db-name" bind:value={newDbName} placeholder="main-db" required pattern="[a-z0-9][a-z0-9.-]*" />
			</div>
			<Button type="submit" disabled={creatingDb}>{creatingDb ? 'Creating…' : 'Create database'}</Button>
		</form>
		{#if dbLines.length}
			<div class="bg-card mt-2 max-h-48 overflow-y-auto rounded-md border p-3 font-mono text-xs leading-5">
				{#each dbLines as line, i (i)}<div class="whitespace-pre-wrap">{line}</div>{/each}
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
