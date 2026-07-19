<script lang="ts">
	import { onMount } from 'svelte';
	import { api, stream } from '$lib/api';
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
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import InfoTip from '$lib/components/info-tip.svelte';

	type Service = { type: string; name: string; status: string; category: string; links: string[] };

	const DB_TYPES = [
		{ type: 'postgres', label: 'Postgres', blurb: 'SQL database, best default for most apps' },
		{ type: 'mysql', label: 'MySQL', blurb: 'SQL database' },
		{ type: 'mariadb', label: 'MariaDB', blurb: 'MySQL-compatible SQL database' },
		{ type: 'redis', label: 'Redis', blurb: 'In-memory cache and queues' },
		{ type: 'mongo', label: 'Mongo', blurb: 'Document database' }
	] as const;

	let services = $state<Service[]>([]);
	let categories = $state<string[]>([]);
	let appNames = $state<string[]>([]);
	let plugins = $state<Record<string, boolean>>({});
	let loading = $state(true);

	// create dialog
	let createOpen = $state(false);
	let newType = $state('postgres');
	let newName = $state('');
	let newCategory = $state('');
	let creating = $state(false);
	let createLines = $state<string[]>([]);

	// install plugin dialog
	let installOpen = $state(false);
	let installType = $state('');
	let installing = $state(false);
	let installLines = $state<string[]>([]);

	// category dialog
	let newCatOpen = $state(false);
	let newCatName = $state('');
	let creatingCat = $state(false);

	let dragIdx = $state(-1);

	const installedTypes = $derived(DB_TYPES.filter((t) => plugins[t.type]));
	const missingTypes = $derived(DB_TYPES.filter((t) => !plugins[t.type]));
	const anyPlugin = $derived(installedTypes.length > 0);

	const groups = $derived.by(() => {
		const g: Record<string, Service[]> = {};
		for (const c of categories) g[c] ??= [];
		for (const s of services) (g[s.category || 'Uncategorised'] ??= []).push(s);
		return categories
			.filter((c) => c !== 'Uncategorised' || g[c]?.length)
			.map((c) => [c, g[c] ?? []] as [string, Service[]]);
	});

	async function moveCategory(from: number, to: number) {
		if (from === to || from < 0 || to < 0 || to >= categories.length) return;
		const next = [...categories];
		const [c] = next.splice(from, 1);
		next.splice(to, 0, c);
		categories = next;
		await api('/dbcategories/order', { method: 'PUT', body: JSON.stringify({ names: next }) }).catch((e) =>
			toast.error(msg(e))
		);
	}

	function nudge(category: string, dir: number) {
		const i = categories.indexOf(category);
		moveCategory(i, i + dir);
	}

	async function destroyService(s: Service) {
		if (!(await askConfirm(`Destroy ${s.type} database "${s.name}"? Its data is permanently deleted.`))) return;
		try {
			await api('/services', { method: 'DELETE', body: JSON.stringify({ type: s.type, name: s.name }) });
			toast.success(`Destroyed ${s.name}`);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	async function load() {
		loading = true;
		try {
			const d = await api('/services');
			services = d.services ?? [];
			const cats: string[] = d.categories ?? [];
			categories = cats.includes('Uncategorised') ? cats : ['Uncategorised', ...cats];
			plugins = d.plugins ?? {};
			const a = await api('/apps');
			appNames = (a.apps ?? []).map((x: { name: string }) => x.name);
		} finally {
			loading = false;
		}
	}

	async function setLink(s: Service, app: string, unlink: boolean) {
		try {
			await api('/services/link', {
				method: 'POST',
				body: JSON.stringify({ type: s.type, name: s.name, app, unlink })
			});
			toast.success(
				unlink
					? `Unlinked ${app} from ${s.name}`
					: `Linked ${s.name} to ${app}, its connection URL is now in the app's env`
			);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}
	onMount(load);

	function quickCreate(type: string, category = '') {
		if (!plugins[type]) {
			startInstall(type);
			return;
		}
		newType = type;
		newName = '';
		newCategory = category;
		createLines = [];
		createOpen = true;
	}

	function startInstall(type: string) {
		installType = type;
		installLines = [];
		installing = false;
		installOpen = true;
	}

	function runInstall() {
		installing = true;
		installLines = [];
		stream(
			'/services/plugins',
			(l) => {
				installLines.push(l);
			},
			{ method: 'POST', body: JSON.stringify({ type: installType }) },
			async () => {
				installing = false;
				const ok = installLines.some((l) => l.includes('[gantry] done'));
				const err = installLines.find((l) => l.includes('[gantry] error'));
				if (ok && !err) {
					toast.success(`${installType} plugin installed`);
					plugins = { ...plugins, [installType]: true };
					await load();
				} else if (err) {
					toast.error(err.replace('[gantry] error: ', ''));
				}
			}
		);
	}

	function create(e: SubmitEvent) {
		e.preventDefault();
		creating = true;
		createLines = [];
		const name = newName.trim();
		const category = newCategory.trim();
		stream(
			'/services',
			(l) => {
				createLines.push(l);
			},
			{ method: 'POST', body: JSON.stringify({ type: newType, name }) },
			async () => {
				creating = false;
				if (category) {
					await api('/services/category', {
						method: 'POST',
						body: JSON.stringify({ type: newType, name, category })
					}).catch(() => {});
				}
				await load();
			}
		);
	}

	async function setCategory(s: Service, category: string) {
		try {
			await api('/services/category', {
				method: 'POST',
				body: JSON.stringify({ type: s.type, name: s.name, category })
			});
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	async function createCategory(e: SubmitEvent) {
		e.preventDefault();
		creatingCat = true;
		try {
			await api('/dbcategories', { method: 'POST', body: JSON.stringify({ name: newCatName.trim() }) });
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

	async function deleteCategory(name: string) {
		if (!(await askConfirm(`Delete category "${name}"? Databases in it move to Uncategorised.`))) return;
		try {
			await api('/dbcategories', { method: 'DELETE', body: JSON.stringify({ name }) });
			toast.success(`Deleted "${name}"`);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	function typeLabel(type: string) {
		return DB_TYPES.find((t) => t.type === type)?.label ?? type;
	}
</script>

<div class="mx-auto max-w-5xl">
	<div class="mb-4 flex flex-wrap items-center gap-2">
		<h1 class="flex items-center gap-1.5 text-2xl font-semibold tracking-tight">
			Databases
			<InfoTip
				text="Each type needs its dokku plugin on the server. Install plugins you need below (they use almost no resources until you create a database). Creating a database pulls a Docker image and runs a container."
			/>
		</h1>
		<div class="ml-auto flex flex-wrap gap-2">
			<Button variant="outline" size="sm" onclick={load} disabled={loading}>
				<RefreshCwIcon class="size-4 {loading ? 'animate-spin' : ''}" /> Refresh
			</Button>
			<Button variant="outline" size="sm" onclick={() => (newCatOpen = true)}>
				<FolderPlusIcon class="size-4" /> New category
			</Button>
		</div>
	</div>

	<!-- Ready types: create a database -->
	<section class="mb-6">
		<div class="mb-2 flex items-center gap-2">
			<h2 class="text-sm font-medium">Create database</h2>
			<span class="text-muted-foreground text-xs">plugins installed on this server</span>
		</div>
		{#if loading && !Object.keys(plugins).length}
			<div class="flex flex-wrap gap-2">
				{#each Array(3) as _, i (i)}<Skeleton class="h-9 w-28" />{/each}
			</div>
		{:else if anyPlugin}
			<div class="flex flex-wrap gap-2">
				{#each installedTypes as t (t.type)}
					<Button variant="outline" size="sm" onclick={() => quickCreate(t.type)} title={t.blurb}>
						<DatabaseIcon class="size-4" />
						{t.label}
						<PlusIcon class="text-muted-foreground size-3.5" />
					</Button>
				{/each}
			</div>
		{:else}
			<p class="text-muted-foreground text-sm">
				No database plugins installed yet. Install one below, then create a database.
			</p>
		{/if}
	</section>

	<!-- Missing plugins: install on demand -->
	{#if missingTypes.length}
		<section class="mb-8">
			<div class="mb-2 flex items-center gap-2">
				<h2 class="text-sm font-medium">Install plugin</h2>
				<span class="text-muted-foreground text-xs">one-time setup, free until you create a DB</span>
			</div>
			<div class="flex flex-wrap gap-2">
				{#each missingTypes as t (t.type)}
					<Button
						variant="outline"
						size="sm"
						class="border-dashed"
						onclick={() => startInstall(t.type)}
						title={t.blurb}
					>
						<DownloadIcon class="size-4" />
						{t.label}
						<span
							class="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-500"
						>
							not installed
						</span>
					</Button>
				{/each}
			</div>
		</section>
	{/if}

	{#if loading && !services.length}
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
				{#if anyPlugin}
					<button
						class="text-muted-foreground hover:text-foreground"
						onclick={() =>
							quickCreate(
								plugins.postgres ? 'postgres' : installedTypes[0]?.type ?? 'postgres',
								category === 'Uncategorised' ? '' : category
							)}
						title="New database in {category}"
						aria-label="New database in {category}"
					>
						<PlusIcon class="size-4" />
					</button>
				{/if}
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
					{#each list as s (s.type + s.name)}
						<Card.Root>
							<Card.Header>
								<Card.Title class="flex items-center gap-2 text-base">
									<DatabaseIcon class="text-muted-foreground size-4" />
									{s.name}
									<Badge variant="secondary" class="ml-auto">{s.type}</Badge>
									<button
										class="text-muted-foreground hover:text-destructive"
										onclick={() => destroyService(s)}
										aria-label="Destroy {s.name}"
									>
										<Trash2Icon class="size-4" />
									</button>
								</Card.Title>
								<Card.Description class="flex items-center justify-between gap-2">
									{s.status}
									<select
										class="border-input text-muted-foreground h-7 rounded-md border bg-transparent px-2 text-xs"
										value={s.category}
										onchange={(e) => setCategory(s, (e.target as HTMLSelectElement).value)}
									>
										<option value="">Uncategorised</option>
										{#each categories as c (c)}
											<option value={c} selected={c === s.category}>{c}</option>
										{/each}
									</select>
								</Card.Description>
							</Card.Header>
							<Card.Content class="flex flex-wrap items-center gap-1.5">
								{#each s.links ?? [] as app (app)}
									<Badge variant="outline" class="gap-1 font-mono text-xs">
										{app}
										<button
											class="text-muted-foreground hover:text-destructive"
											onclick={() => setLink(s, app, true)}
											aria-label="Unlink {app}"
										>
											×
										</button>
									</Badge>
								{/each}
								{#if appNames.filter((a) => !(s.links ?? []).includes(a)).length}
									<select
										class="border-input text-muted-foreground h-7 rounded-md border bg-transparent px-2 text-xs"
										value=""
										onchange={(e) => {
											const app = (e.target as HTMLSelectElement).value;
											if (app) setLink(s, app, false);
											(e.target as HTMLSelectElement).value = '';
										}}
									>
										<option value="">Link app…</option>
										{#each appNames.filter((a) => !(s.links ?? []).includes(a)) as a (a)}
											<option value={a}>{a}</option>
										{/each}
									</select>
								{/if}
							</Card.Content>
						</Card.Root>
					{/each}
				</div>
			{:else}
				<p class="text-muted-foreground text-sm">No databases in this category yet.</p>
			{/if}
		{:else}
			<p class="text-muted-foreground text-sm">
				{#if anyPlugin}
					No databases yet. Use Create database above, then link each one to an app so it gets a
					connection URL in env.
				{:else}
					Install a database plugin above to get started.
				{/if}
			</p>
		{/each}
	{/if}
</div>

<Dialog.Root bind:open={createOpen}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>New {typeLabel(newType)} database</Dialog.Title>
			<Dialog.Description>
				Creates a {newType} service on this server. After it is up, link it to an app so the
				connection URL is written into that app's environment.
			</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={create} class="grid gap-4">
			<div class="grid gap-2">
				<Label for="db-name">Name</Label>
				<Input
					id="db-name"
					bind:value={newName}
					placeholder="main-db"
					required
					pattern="[a-z0-9][a-z0-9.-]*"
				/>
			</div>
			<div class="grid gap-2">
				<Label for="db-cat">Category <span class="text-muted-foreground">(optional)</span></Label>
				<Input id="db-cat" bind:value={newCategory} placeholder="e.g. Production" list="dbcats" />
				<datalist id="dbcats">
					{#each categories as c (c)}<option value={c}></option>{/each}
				</datalist>
			</div>
			<Button type="submit" disabled={creating}>{creating ? 'Creating…' : 'Create database'}</Button>
		</form>
		{#if createLines.length}
			<div class="bg-card mt-2 max-h-48 overflow-y-auto rounded-md border p-3 font-mono text-xs leading-5">
				{#each createLines as line, i (i)}<div class="whitespace-pre-wrap">{line}</div>{/each}
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={installOpen}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>Install {typeLabel(installType)} plugin</Dialog.Title>
			<Dialog.Description>
				One-time install of the dokku {installType} plugin on this server. Uses almost no disk or
				memory until you create a database. First create pulls a Docker image.
			</Dialog.Description>
		</Dialog.Header>
		<div class="grid gap-3">
			{#if !installing && !installLines.length}
				<Button onclick={runInstall}>
					<DownloadIcon class="size-4" /> Install {typeLabel(installType)}
				</Button>
			{:else if installing}
				<p class="text-muted-foreground text-sm">Installing… this can take a minute on first run.</p>
			{:else if installLines.some((l) => l.includes('[gantry] done')) && !installLines.some((l) => l.includes('[gantry] error'))}
				<p class="text-sm text-emerald-500">Installed. You can create a {installType} database now.</p>
				<Button
					onclick={() => {
						installOpen = false;
						quickCreate(installType);
					}}
				>
					Create {typeLabel(installType)} database
				</Button>
			{/if}
			{#if installLines.length}
				<div class="bg-card max-h-48 overflow-y-auto rounded-md border p-3 font-mono text-xs leading-5">
					{#each installLines as line, i (i)}<div class="whitespace-pre-wrap">{line}</div>{/each}
				</div>
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={newCatOpen}>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title>New database category</Dialog.Title>
		</Dialog.Header>
		<form onsubmit={createCategory} class="grid gap-4">
			<Input bind:value={newCatName} required placeholder="e.g. Production" />
			<Button type="submit" disabled={creatingCat}>{creatingCat ? 'Creating…' : 'Create category'}</Button>
		</form>
	</Dialog.Content>
</Dialog.Root>
