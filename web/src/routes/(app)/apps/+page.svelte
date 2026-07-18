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
	import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';

	type App = { name: string; running: boolean; category: string };

	let apps = $state<App[]>([]);
	let categories = $state<string[]>([]);
	let loading = $state(true);

	// new app dialog
	let newAppOpen = $state(false);
	let newAppName = $state('');
	let newAppCategory = $state('');
	let newAppSource = $state<'empty' | 'repo' | 'image'>('empty');
	let newAppRepo = $state('');
	let newAppRef = $state('');
	let newAppDockerfile = $state('');
	let newAppImage = $state('');
	let creatingApp = $state(false);
	let createLines = $state<string[]>([]);

	// new category dialog
	let newCatOpen = $state(false);
	let newCatName = $state('');
	let creatingCat = $state(false);

	const groups = $derived.by(() => {
		const g: Record<string, App[]> = {};
		for (const c of categories) g[c] ??= [];
		for (const a of apps) (g[a.category || 'Uncategorised'] ??= []).push(a);
		return Object.entries(g).sort(([a], [b]) => a.localeCompare(b));
	});

	async function load() {
		loading = true;
		try {
			const d = await api('/apps');
			apps = d.apps ?? [];
			categories = d.categories ?? [];
		} finally {
			loading = false;
		}
	}
	onMount(load);

	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	async function createApp(e: SubmitEvent) {
		e.preventDefault();
		creatingApp = true;
		const n = newAppName.trim();
		try {
			await api('/apps', {
				method: 'POST',
				body: JSON.stringify({ name: n, category: newAppCategory.trim() })
			});
		} catch (e) {
			toast.error(msg(e));
			creatingApp = false;
			return;
		}
		if (newAppSource === 'empty') {
			toast.success(`Created ${n}`);
			newAppOpen = false;
			creatingApp = false;
			goto('/app/' + n);
			return;
		}
		const body =
			newAppSource === 'repo'
				? { repo: newAppRepo.trim(), ref: newAppRef.trim(), dockerfile: newAppDockerfile.trim() }
				: { image: newAppImage.trim() };
		createLines = [];
		stream(
			`/apps/${n}/deploy`,
			(l) => {
				createLines.push(l);
			},
			{ method: 'POST', body: JSON.stringify(body) },
			() => {
				creatingApp = false;
				newAppOpen = false;
				goto('/app/' + n);
			}
		);
	}

	async function deleteCategory(name: string) {
		if (!confirm(`Delete category "${name}"? Apps in it move to Uncategorised.`)) return;
		try {
			await api('/categories', { method: 'DELETE', body: JSON.stringify({ name }) });
			toast.success(`Deleted "${name}"`);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	async function createCategory(e: SubmitEvent) {
		e.preventDefault();
		creatingCat = true;
		try {
			await api('/categories', { method: 'POST', body: JSON.stringify({ name: newCatName.trim() }) });
			toast.success(`Category "${newCatName.trim()}" created`);
			newCatOpen = false;
			newCatName = '';
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			creatingCat = false;
		}
	}

	const srcInput =
		'border-input bg-transparent dark:bg-input/30 h-9 rounded-md border px-3 text-sm shadow-xs';
</script>

<div class="mx-auto max-w-5xl">
	<div class="mb-6 flex flex-wrap items-center gap-2">
		<h1 class="text-2xl font-semibold tracking-tight">Apps</h1>
		<div class="ml-auto flex flex-wrap gap-2">
			<Button variant="outline" size="sm" onclick={load} disabled={loading}>
				<RefreshCwIcon class="size-4 {loading ? 'animate-spin' : ''}" /> Refresh
			</Button>
			<Button variant="outline" size="sm" onclick={() => (newCatOpen = true)}>
				<FolderPlusIcon class="size-4" /> New category
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
			<div class="mt-8 mb-3 flex items-center gap-3 border-b pb-2 first:mt-0">
				<h2 class="text-lg font-semibold tracking-tight">{category}</h2>
				<button
					class="text-muted-foreground hover:text-foreground"
					onclick={() => {
						newAppCategory = category === 'Uncategorised' ? '' : category;
						newAppOpen = true;
					}}
					title="New app in {category}"
					aria-label="New app in {category}"
				>
					<PlusIcon class="size-4" />
				</button>
				{#if category !== 'Uncategorised'}
					<button
						class="text-muted-foreground hover:text-destructive"
						onclick={() => deleteCategory(category)}
						title="Delete category"
						aria-label="Delete category {category}"
					>
						<Trash2Icon class="size-4" />
					</button>
				{/if}
			</div>
			{#if list.length}
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
				<p class="text-muted-foreground text-sm">No apps in this category yet.</p>
			{/if}
		{:else}
			<p class="text-muted-foreground text-sm">No apps yet — create one to get started.</p>
		{/each}
	{/if}
</div>

<Dialog.Root bind:open={newAppOpen}>
	<Dialog.Content class="max-w-lg">
		<Dialog.Header>
			<Dialog.Title>New app</Dialog.Title>
			<Dialog.Description>Create the app and optionally deploy it right away.</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={createApp} class="grid gap-4">
			<div class="grid grid-cols-2 gap-3">
				<div class="grid gap-2">
					<Label for="app-name">Name</Label>
					<Input id="app-name" bind:value={newAppName} placeholder="my-api" required pattern="[a-z0-9][a-z0-9.-]*" />
				</div>
				<div class="grid gap-2">
					<Label for="app-cat">Category</Label>
					<Input id="app-cat" bind:value={newAppCategory} placeholder="e.g. Websites" list="cats" />
					<datalist id="cats">
						{#each categories as c (c)}<option value={c}></option>{/each}
					</datalist>
				</div>
			</div>
			<div class="grid gap-2">
				<Label for="app-src">Deploy from</Label>
				<select id="app-src" bind:value={newAppSource} class={srcInput}>
					<option value="empty">Nothing yet — empty app</option>
					<option value="repo">GitHub repository (builds on the server)</option>
					<option value="image">Docker image (registry)</option>
				</select>
			</div>
			{#if newAppSource === 'repo'}
				<div class="grid gap-2">
					<Label for="app-repo">Repository URL</Label>
					<Input id="app-repo" bind:value={newAppRepo} required placeholder="https://github.com/you/repo" class="font-mono text-xs" />
				</div>
				<div class="grid grid-cols-2 gap-3">
					<div class="grid gap-2">
						<Label for="app-ref">Branch <span class="text-muted-foreground">(optional)</span></Label>
						<Input id="app-ref" bind:value={newAppRef} placeholder="main" class="font-mono text-xs" />
					</div>
					<div class="grid gap-2">
						<Label for="app-df">Dockerfile path <span class="text-muted-foreground">(optional)</span></Label>
						<Input id="app-df" bind:value={newAppDockerfile} placeholder="Dockerfile" class="font-mono text-xs" />
					</div>
				</div>
				<p class="text-muted-foreground text-xs">
					Private repo? Save your GitHub username + token in Settings first. No Dockerfile in the repo
					means dokku builds with buildpacks (auto-detected).
				</p>
			{:else if newAppSource === 'image'}
				<div class="grid gap-2">
					<Label for="app-img">Image</Label>
					<Input id="app-img" bind:value={newAppImage} required placeholder="ghcr.io/you/app:latest" class="font-mono text-xs" />
				</div>
			{/if}
			<Button type="submit" disabled={creatingApp}>
				{creatingApp ? 'Working…' : newAppSource === 'empty' ? 'Create app' : 'Create & deploy'}
			</Button>
		</form>
		{#if createLines.length}
			<div class="bg-card mt-2 max-h-64 overflow-y-auto rounded-md border p-3 font-mono text-xs leading-5">
				{#each createLines as line, i (i)}<div class="whitespace-pre-wrap">{line}</div>{/each}
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={newCatOpen}>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title>New category</Dialog.Title>
			<Dialog.Description>Groups apps on this page — assign apps to it when creating or from their page.</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={createCategory} class="grid gap-4">
			<Input bind:value={newCatName} required placeholder="e.g. Client sites" />
			<Button type="submit" disabled={creatingCat}>{creatingCat ? 'Creating…' : 'Create category'}</Button>
		</form>
	</Dialog.Content>
</Dialog.Root>

