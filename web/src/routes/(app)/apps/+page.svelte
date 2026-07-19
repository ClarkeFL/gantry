<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { askConfirm } from '$lib/confirm.svelte';
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
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';

	type App = {
		name: string;
		running: boolean;
		category: string;
		lastDeploy?: string;
		lastDeployOk: boolean;
	};

	let apps = $state<App[]>([]);
	let categories = $state<string[]>([]);
	let loading = $state(true);

	// new app dialog
	let newAppOpen = $state(false);
	let newAppName = $state('');
	let newAppCategory = $state('');
	let creatingApp = $state(false);

	// new category dialog
	let newCatOpen = $state(false);
	let newCatName = $state('');
	let creatingCat = $state(false);

	let dragIdx = $state(-1);

	const groups = $derived.by(() => {
		const g: Record<string, App[]> = {};
		for (const c of categories) g[c] ??= [];
		for (const a of apps) (g[a.category || 'Uncategorised'] ??= []).push(a);
		return categories
			.filter((c) => c !== 'Uncategorised' || g[c]?.length)
			.map((c) => [c, g[c] ?? []] as [string, App[]]);
	});

	async function moveCategory(from: number, to: number) {
		if (from === to || from < 0 || to < 0 || to >= categories.length) return;
		const next = [...categories];
		const [c] = next.splice(from, 1);
		next.splice(to, 0, c);
		categories = next;
		await api('/categories/order', { method: 'PUT', body: JSON.stringify({ names: next }) }).catch((e) =>
			toast.error(msg(e))
		);
	}

	function nudge(category: string, dir: number) {
		const i = categories.indexOf(category);
		moveCategory(i, i + dir);
	}

	async function load() {
		loading = true;
		try {
			const d = await api('/apps');
			apps = d.apps ?? [];
			const cats: string[] = d.categories ?? [];
			categories = cats.includes('Uncategorised') ? cats : ['Uncategorised', ...cats];
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
			toast.success(`Created ${n} — pick a source to deploy`);
			newAppOpen = false;
			goto(`/app/${n}?tab=source`);
		} catch (e) {
			toast.error(msg(e));
		} finally {
			creatingApp = false;
		}
	}

	async function deleteCategory(name: string) {
		if (!(await askConfirm(`Delete category "${name}"? Apps in it move to Uncategorised.`))) return;
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
			<div
				class="mt-8 mb-3 flex items-center gap-3 border-b pb-2 first:mt-0"
				role="listitem"
				draggable={true}
				ondragstart={(e) => {
					dragIdx = categories.indexOf(category);
					e.dataTransfer?.setData('text/plain', category);
				}}
				ondragover={(e) => e.preventDefault()}
				ondrop={(e) => {
					e.preventDefault();
					moveCategory(dragIdx, categories.indexOf(category));
					dragIdx = -1;
				}}
			>
				<span class="flex flex-col">
					<button
						class="text-muted-foreground hover:text-foreground disabled:opacity-30"
						onclick={() => nudge(category, -1)}
						disabled={categories.indexOf(category) === 0}
						aria-label="Move {category} up"
					>
						<ChevronUpIcon class="size-3.5" />
					</button>
					<button
						class="text-muted-foreground hover:text-foreground disabled:opacity-30"
						onclick={() => nudge(category, 1)}
						disabled={categories.indexOf(category) === categories.length - 1}
						aria-label="Move {category} down"
					>
						<ChevronDownIcon class="size-3.5" />
					</button>
				</span>
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
									<Card.Description class="flex items-center gap-2">
										{app.running ? 'Running' : 'Stopped'}
										{#if app.lastDeploy && !app.lastDeployOk}
											<span class="rounded bg-red-500/15 px-1.5 py-0.5 text-xs font-medium text-red-500">
												last deploy failed
											</span>
										{/if}
									</Card.Description>
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
			<Dialog.Description>You'll pick a deploy source on the app's Source tab next.</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={createApp} class="grid gap-4">
			<div class="grid grid-cols-2 gap-3">
				<div class="grid gap-2">
					<Label for="app-name">Name</Label>
					<Input
						id="app-name"
						bind:value={newAppName}
						placeholder="my-api"
						required
						pattern="[a-z0-9][a-z0-9.-]*"
						autocapitalize="off"
						autocorrect="off"
						spellcheck={false}
					/>
				</div>
				<div class="grid gap-2">
					<Label for="app-cat">Category</Label>
					<Input id="app-cat" bind:value={newAppCategory} placeholder="e.g. Websites" list="cats" />
					<datalist id="cats">
						{#each categories as c (c)}<option value={c}></option>{/each}
					</datalist>
				</div>
			</div>
			<Button type="submit" disabled={creatingApp}>{creatingApp ? 'Creating…' : 'Create app'}</Button>
		</form>
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

