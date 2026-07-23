<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { flip } from 'svelte/animate';
	import { crossfade } from 'svelte/transition';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
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
	import EnvEditor from '$lib/components/env-editor.svelte';
	import InfoTip from '$lib/components/info-tip.svelte';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import BoxIcon from '@lucide/svelte/icons/box';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
	import KeyIcon from '@lucide/svelte/icons/key-round';

	type App = { name: string; running: boolean; category: string; group: string; lastDeploy?: string; lastDeployOk: boolean; maintenance: boolean };
	type Service = { type: string; name: string; status: string; category: string; links: string[] };

	const DB_TYPES = [
		{ type: 'postgres', label: 'Postgres', blurb: 'SQL database, best default for most apps' },
		{ type: 'mysql', label: 'MySQL', blurb: 'SQL database' },
		{ type: 'mariadb', label: 'MariaDB', blurb: 'MySQL-compatible SQL database' },
		{ type: 'redis', label: 'Redis', blurb: 'In-memory cache and queues' },
		{ type: 'mongo', label: 'Mongo', blurb: 'Document database' }
	] as const;

	const project = $derived(decodeURIComponent(page.params.name!));

	let apps = $state<App[]>([]);
	let services = $state<Service[]>([]);
	let projects = $state<string[]>([]);
	let plugins = $state<Record<string, boolean>>({});
	let env = $state<Record<string, string>>({});
	let groups = $state<string[]>([]);
	let ctrStats = $state<Record<string, { cpu: string; mem: string; net: string }>>({});
	let statsTimer: ReturnType<typeof setInterval>;
	let envOpen = $state(false);
	let loading = $state(true);

	// new group dialog
	let newGroupOpen = $state(false);
	let newGroupName = $state('');
	let creatingGroup = $state(false);

	// rename group dialog
	let renameGroupOpen = $state(false);
	let renameGroupFrom = $state('');
	let renameGroupTo = $state('');
	let renamingGroup = $state(false);

	// rename dialog
	let renameOpen = $state(false);
	let renameTo = $state('');
	let renaming = $state(false);

	// new app dialog; newAppGroup preselects the group the app is created in
	let newAppOpen = $state(false);
	let newAppName = $state('');
	let newAppGroup = $state('');
	let creatingApp = $state(false);

	// create database dialog
	let createOpen = $state(false);
	let newType = $state('postgres');
	let newDbName = $state('');
	let creatingDb = $state(false);
	let createLines = $state<string[]>([]);

	// install plugin dialog
	let installOpen = $state(false);
	let installType = $state('');
	let installing = $state(false);
	let installLines = $state<string[]>([]);

	// drag a card onto a group to move it; crossfade + flip animate the move
	const [dndSend, dndReceive] = crossfade({ duration: 250 });
	let draggingApp = $state('');
	let dragOverZone = $state<string | null>(null);

	function dragStart(e: DragEvent, app: string) {
		draggingApp = app;
		e.dataTransfer!.effectAllowed = 'move';
		e.dataTransfer!.setData('text/plain', app);
	}
	function dragEnd() {
		draggingApp = '';
		dragOverZone = null;
	}
	function dragOver(e: DragEvent, zone: string) {
		if (!draggingApp) return;
		e.preventDefault();
		e.dataTransfer!.dropEffect = 'move';
		dragOverZone = zone;
	}
	function dragLeave(e: DragEvent, zone: string) {
		if (dragOverZone === zone && !(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) {
			dragOverZone = null;
		}
	}
	async function dropApp(e: DragEvent, group: string) {
		e.preventDefault();
		const name = draggingApp;
		dragEnd();
		const a = apps.find((x) => x.name === name);
		if (!a || (a.group ?? '') === group) return;
		a.group = group; // optimistic, so the card animates over immediately
		try {
			await api(`/apps/${name}/group`, { method: 'POST', body: JSON.stringify({ group }) });
		} catch (err) {
			toast.error(msg(err));
			await load();
		}
	}

	const myApps = $derived(apps.filter((a) => a.category === project));
	const myServices = $derived(services.filter((s) => s.category === project));
	// apps whose group no longer exists fall back to the base section
	const baseApps = $derived(myApps.filter((a) => !a.group || !groups.includes(a.group)));
	const appsInGroup = (g: string) => myApps.filter((a) => a.group === g);
	const installedTypes = $derived(DB_TYPES.filter((t) => plugins[t.type]));
	const missingTypes = $derived(DB_TYPES.filter((t) => !plugins[t.type]));

	function linkedDbs(app: string) {
		return myServices.filter((s) => (s.links ?? []).includes(app));
	}

	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	async function load() {
		loading = true;
		try {
			const [a, s, p] = await Promise.all([
				api('/apps'),
				api('/services'),
				api(`/projects/${encodeURIComponent(project)}`)
			]);
			apps = a.apps ?? [];
			services = s.services ?? [];
			projects = a.categories ?? [];
			plugins = s.plugins ?? {};
			env = p.env ?? {};
			groups = p.groups ?? [];
		} finally {
			loading = false;
		}
	}
	async function loadStats() {
		try {
			const d = await api('/stats');
			const m: Record<string, { cpu: string; mem: string; net: string }> = {};
			for (const a of d.apps ?? []) m[a.app] = { cpu: a.cpu, mem: a.mem, net: a.net };
			ctrStats = m;
		} catch {
			// transient, next poll retries
		}
	}
	onMount(() => {
		load().catch((e) => toast.error(msg(e)));
		loadStats();
		statsTimer = setInterval(loadStats, 10_000);
	});
	onDestroy(() => clearInterval(statsTimer));

	async function saveEnv(set: Record<string, string>, unset: string[], restart: boolean) {
		try {
			const res = await api(`/projects/${encodeURIComponent(project)}/env`, {
				method: 'POST',
				body: JSON.stringify({ set, unset, restart })
			});
			toast.success(
				`Shared variables saved and applied to ${res.applied} ${res.applied === 1 ? 'app' : 'apps'}` +
					(restart ? ', restarting' : '')
			);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	async function createApp(e: SubmitEvent) {
		e.preventDefault();
		creatingApp = true;
		const n = newAppName.trim();
		try {
			await api('/apps', {
				method: 'POST',
				body: JSON.stringify({ name: n, category: project, group: newAppGroup })
			});
			toast.success(`Created ${n}, pick a source to deploy`);
			newAppOpen = false;
			goto(`/app/${n}?tab=source`);
		} catch (e) {
			toast.error(msg(e));
		} finally {
			creatingApp = false;
		}
	}

	async function renameProject(e: SubmitEvent) {
		e.preventDefault();
		const to = renameTo.trim();
		if (!to || to === project) {
			renameOpen = false;
			return;
		}
		renaming = true;
		try {
			await api('/projects/rename', { method: 'PUT', body: JSON.stringify({ from: project, to }) });
			toast.success(`Renamed to ${to}`);
			renameOpen = false;
			goto(`/project/${encodeURIComponent(to)}`, { replaceState: true });
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			renaming = false;
		}
	}

	async function createGroup(e: SubmitEvent) {
		e.preventDefault();
		creatingGroup = true;
		try {
			await api(`/projects/${encodeURIComponent(project)}/groups`, {
				method: 'POST',
				body: JSON.stringify({ name: newGroupName.trim() })
			});
			toast.success(`Group "${newGroupName.trim()}" created`);
			newGroupOpen = false;
			newGroupName = '';
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			creatingGroup = false;
		}
	}

	async function renameGroup(e: SubmitEvent) {
		e.preventDefault();
		const to = renameGroupTo.trim();
		if (!to || to === renameGroupFrom) {
			renameGroupOpen = false;
			return;
		}
		renamingGroup = true;
		try {
			await api(`/projects/${encodeURIComponent(project)}/groups`, {
				method: 'PUT',
				body: JSON.stringify({ from: renameGroupFrom, to })
			});
			toast.success(`Renamed to ${to}`);
			renameGroupOpen = false;
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			renamingGroup = false;
		}
	}

	async function deleteGroup(name: string) {
		if (!(await askConfirm(`Delete group "${name}"? Its apps stay in the project, just ungrouped.`))) return;
		try {
			await api(`/projects/${encodeURIComponent(project)}/groups`, {
				method: 'DELETE',
				body: JSON.stringify({ name })
			});
			toast.success(`Deleted "${name}"`);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	async function setServiceProject(s: Service, p: string) {
		try {
			await api('/services/category', {
				method: 'POST',
				body: JSON.stringify({ type: s.type, name: s.name, category: p })
			});
			toast.success(p ? `Moved ${s.name} to ${p}` : `Removed ${s.name} from ${project}`);
			await load();
		} catch (e) {
			toast.error(msg(e));
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

	function quickCreate(type: string) {
		if (!plugins[type]) {
			startInstall(type);
			return;
		}
		newType = type;
		newDbName = '';
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

	function createDb(e: SubmitEvent) {
		e.preventDefault();
		creatingDb = true;
		createLines = [];
		const name = newDbName.trim();
		stream(
			'/services',
			(l) => {
				createLines.push(l);
			},
			{ method: 'POST', body: JSON.stringify({ type: newType, name }) },
			async () => {
				creatingDb = false;
				await api('/services/category', {
					method: 'POST',
					body: JSON.stringify({ type: newType, name, category: project })
				}).catch(() => {});
				await load();
			}
		);
	}

	function typeLabel(type: string) {
		return DB_TYPES.find((t) => t.type === type)?.label ?? type;
	}
</script>

<div class="mx-auto max-w-5xl">
	<a href="/projects" class="text-muted-foreground hover:text-foreground mb-4 inline-flex items-center gap-1 text-sm">
		<ArrowLeftIcon class="size-4" /> Projects
	</a>

	<div class="mb-6 flex flex-wrap items-center gap-2">
		<h1 class="text-2xl font-semibold tracking-tight">{project}</h1>
		<button
			class="text-muted-foreground hover:text-foreground p-1"
			onclick={() => {
				renameTo = project;
				renameOpen = true;
			}}
			title="Rename project"
			aria-label="Rename project"
		>
			<PencilIcon class="size-4" />
		</button>
		<div class="ml-auto flex flex-wrap gap-2">
			<Button variant="outline" size="sm" onclick={load} disabled={loading}>
				<RefreshCwIcon class="size-4 {loading ? 'animate-spin' : ''}" /> Refresh
			</Button>
			<Button variant="outline" size="sm" onclick={() => (envOpen = true)}>
				<KeyIcon class="size-4" /> Shared env
				<Badge variant="secondary">{Object.keys(env).length}</Badge>
			</Button>
			<Button variant="outline" size="sm" onclick={() => (newGroupOpen = true)}>
				<FolderPlusIcon class="size-4" /> New group
			</Button>
			<Button
				size="sm"
				onclick={() => {
					newAppGroup = '';
					newAppOpen = true;
				}}
			>
				<PlusIcon class="size-4" /> New app
			</Button>
		</div>
	</div>

	{#if loading && !apps.length && !services.length}
		<Skeleton class="h-64" />
	{:else}
		{#snippet appCard(app: App)}
			<a href="/app/{app.name}" class="group block h-full">
			<Card.Root class="group-hover:border-primary/50 h-full transition-colors">
				<Card.Header>
					<Card.Title class="flex items-center gap-2 text-base">
						<BoxIcon class="text-muted-foreground size-4 shrink-0" />
						<span class="truncate">{app.name}</span>
						<span
							class="ml-auto shrink-0 rounded px-1.5 py-0.5 text-xs font-medium {app.running
								? 'bg-emerald-500/15 text-emerald-500'
								: 'bg-red-500/15 text-red-500'}"
						>
							{app.running ? 'running' : 'stopped'}
						</span>
					</Card.Title>
					{#if (app.running && ctrStats[app.name]) || app.maintenance || (app.lastDeploy && !app.lastDeployOk)}
						<Card.Description class="flex flex-wrap items-center gap-x-2 gap-y-1">
							{#if app.running && ctrStats[app.name]}
								<span class="font-mono text-xs" title="CPU, memory and network across all of this app's containers">
									{ctrStats[app.name].cpu} cpu · {ctrStats[app.name].mem}{ctrStats[app.name].net
										? ` · ${ctrStats[app.name].net}`
										: ''}
								</span>
							{/if}
							{#if app.maintenance}
								<span class="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs font-medium text-amber-500">
									maintenance
								</span>
							{/if}
							{#if app.lastDeploy && !app.lastDeployOk}
								<span class="rounded bg-red-500/15 px-1.5 py-0.5 text-xs font-medium text-red-500">
									last deploy failed
								</span>
							{/if}
						</Card.Description>
					{/if}
				</Card.Header>
				{#if linkedDbs(app.name).length}
					<Card.Content class="flex flex-wrap gap-1.5">
						{#each linkedDbs(app.name) as s (s.type + s.name)}
							<Badge variant="outline" class="gap-1 font-mono text-xs">
								<DatabaseIcon class="size-3" />
								{s.name}
							</Badge>
						{/each}
					</Card.Content>
				{/if}
			</Card.Root>
			</a>
		{/snippet}

		<div
			role="list"
			class="mb-6 grid gap-4 rounded-xl transition-all sm:grid-cols-2 lg:grid-cols-3 {dragOverZone === ''
				? 'bg-primary/5 ring-primary/40 p-2 ring-2'
				: ''}"
			ondragover={(e) => dragOver(e, '')}
			ondragleave={(e) => dragLeave(e, '')}
			ondrop={(e) => dropApp(e, '')}
		>
			{#each baseApps as app (app.name)}
				<div
					role="listitem"
					draggable={groups.length > 0}
					ondragstart={(e) => dragStart(e, app.name)}
					ondragend={dragEnd}
					animate:flip={{ duration: 250 }}
					in:dndReceive={{ key: app.name }}
					out:dndSend={{ key: app.name }}
					class="transition-opacity {draggingApp === app.name ? 'opacity-40' : ''}"
				>
					{@render appCard(app)}
				</div>
			{/each}
			<button
				class="text-muted-foreground hover:text-foreground hover:border-muted-foreground/50 flex min-h-24 items-center justify-center gap-2 rounded-xl border border-dashed text-sm transition-colors"
				onclick={() => {
					newAppGroup = '';
					newAppOpen = true;
				}}
			>
				<PlusIcon class="size-4" />
				{draggingApp ? 'Drop here to ungroup' : 'New app'}
			</button>
		</div>
		{#each groups as g (g)}
			<div class="mb-3 flex items-center gap-2">
				<h3 class="text-muted-foreground text-sm font-medium">{g}</h3>
				<button
					class="text-muted-foreground hover:text-foreground"
					onclick={() => {
						renameGroupFrom = g;
						renameGroupTo = g;
						renameGroupOpen = true;
					}}
					title="Rename group"
					aria-label="Rename group {g}"
				>
					<PencilIcon class="size-3.5" />
				</button>
				<button
					class="text-muted-foreground hover:text-destructive"
					onclick={() => deleteGroup(g)}
					title="Delete group"
					aria-label="Delete group {g}"
				>
					<Trash2Icon class="size-3.5" />
				</button>
			</div>
			<div
				role="list"
				class="mb-6 grid gap-4 rounded-xl transition-all sm:grid-cols-2 lg:grid-cols-3 {dragOverZone === g
					? 'bg-primary/5 ring-primary/40 p-2 ring-2'
					: ''}"
				ondragover={(e) => dragOver(e, g)}
				ondragleave={(e) => dragLeave(e, g)}
				ondrop={(e) => dropApp(e, g)}
			>
				{#each appsInGroup(g) as app (app.name)}
					<div
						role="listitem"
						draggable={true}
						ondragstart={(e) => dragStart(e, app.name)}
						ondragend={dragEnd}
						animate:flip={{ duration: 250 }}
						in:dndReceive={{ key: app.name }}
						out:dndSend={{ key: app.name }}
						class="transition-opacity {draggingApp === app.name ? 'opacity-40' : ''}"
					>
						{@render appCard(app)}
					</div>
				{/each}
				<button
					class="text-muted-foreground hover:text-foreground hover:border-muted-foreground/50 flex min-h-24 items-center justify-center gap-2 rounded-xl border border-dashed text-sm transition-colors"
					onclick={() => {
						newAppGroup = g;
						newAppOpen = true;
					}}
				>
					<PlusIcon class="size-4" /> New app
				</button>
			</div>
		{/each}

		<div class="mb-3 flex items-center gap-2 border-b pb-2">
			<DatabaseIcon class="text-muted-foreground size-4" />
			<h2 class="flex items-center gap-1.5 text-lg font-semibold tracking-tight">
				Databases
				<InfoTip
					text="Each type needs its dokku plugin on the server, a one-time install. Creating a database runs a container; linking it to an app writes the connection URL into that app's env."
				/>
			</h2>
		</div>

		<div class="mb-4 flex flex-wrap gap-2">
			{#each installedTypes as t (t.type)}
				<Button variant="outline" size="sm" onclick={() => quickCreate(t.type)} title={t.blurb}>
					<DatabaseIcon class="size-4" />
					{t.label}
					<PlusIcon class="text-muted-foreground size-3.5" />
				</Button>
			{/each}
			{#each missingTypes as t (t.type)}
				<Button variant="outline" size="sm" class="border-dashed" onclick={() => startInstall(t.type)} title={t.blurb}>
					<DownloadIcon class="size-4" />
					{t.label}
					<span class="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-500">
						not installed
					</span>
				</Button>
			{/each}
		</div>

		{#if myServices.length}
			<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
				{#each myServices as s (s.type + s.name)}
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
									onchange={(e) => setServiceProject(s, (e.target as HTMLSelectElement).value)}
								>
									<option value="">Unassigned</option>
									{#each projects as p (p)}
										<option value={p} selected={p === s.category}>{p}</option>
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
							{#if apps.filter((a) => !(s.links ?? []).includes(a.name)).length}
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
									{#each apps.filter((a) => !(s.links ?? []).includes(a.name)) as a (a.name)}
										<option value={a.name}>{a.name}</option>
									{/each}
								</select>
							{/if}
						</Card.Content>
					</Card.Root>
				{/each}
			</div>
		{:else}
			<p class="text-muted-foreground text-sm">
				{#if installedTypes.length}
					No databases in this project yet. Create one above, then link it to an app.
				{:else}
					Install a database plugin above to get started.
				{/if}
			</p>
		{/if}
	{/if}
</div>

<Dialog.Root bind:open={renameOpen}>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title>Rename project</Dialog.Title>
			<Dialog.Description>
				Renames the project everywhere, its apps, databases and shared variables move with it.
			</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={renameProject} class="grid gap-4">
			<Input bind:value={renameTo} required placeholder={project} />
			<Button type="submit" disabled={renaming}>{renaming ? 'Renaming…' : 'Rename'}</Button>
		</form>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={envOpen}>
	<Dialog.Content class="sm:max-w-2xl">
		<Dialog.Header>
			<Dialog.Title>Shared environment variables</Dialog.Title>
			<Dialog.Description>
				Every app in this project gets these variables, so shared keys live in one place. An app
				can still override a key on its own Environment tab, and that override is respected.
			</Dialog.Description>
		</Dialog.Header>
		<EnvEditor {env} restartLabel="Restart apps" onsave={saveEnv} />
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={renameGroupOpen}>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title>Rename group</Dialog.Title>
			<Dialog.Description>Apps in the group move with it.</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={renameGroup} class="grid gap-4">
			<Input bind:value={renameGroupTo} required placeholder={renameGroupFrom} />
			<Button type="submit" disabled={renamingGroup}>{renamingGroup ? 'Renaming…' : 'Rename'}</Button>
		</form>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={newGroupOpen}>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title>New group in {project}</Dialog.Title>
			<Dialog.Description>
				A label to separate apps inside this project, like "Frontend" or "Micro services". It
				doesn't change anything about how apps run.
			</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={createGroup} class="grid gap-4">
			<Input bind:value={newGroupName} required placeholder="e.g. Micro services" />
			<Button type="submit" disabled={creatingGroup}>{creatingGroup ? 'Creating…' : 'Create group'}</Button>
		</form>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={newAppOpen}>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title>New app in {newAppGroup ? `${project} / ${newAppGroup}` : project}</Dialog.Title>
			<Dialog.Description>
				You'll pick a deploy source on the app's Source tab next. It starts with this project's
				shared variables.
			</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={createApp} class="grid gap-4">
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
			<Button type="submit" disabled={creatingApp}>{creatingApp ? 'Creating…' : 'Create app'}</Button>
		</form>
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={createOpen}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>New {typeLabel(newType)} database in {project}</Dialog.Title>
			<Dialog.Description>
				Creates a {newType} service on this server. After it is up, link it to an app so the
				connection URL is written into that app's environment.
			</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={createDb} class="grid gap-4">
			<div class="grid gap-2">
				<Label for="db-name">Name</Label>
				<Input id="db-name" bind:value={newDbName} placeholder="main-db" required pattern="[a-z0-9][a-z0-9.-]*" />
			</div>
			<Button type="submit" disabled={creatingDb}>{creatingDb ? 'Creating…' : 'Create database'}</Button>
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
