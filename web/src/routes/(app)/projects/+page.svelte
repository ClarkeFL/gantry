<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { askConfirm } from '$lib/confirm.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Card from '$lib/components/ui/card';
	import * as Dialog from '$lib/components/ui/dialog';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import BoxIcon from '@lucide/svelte/icons/box';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import FolderIcon from '@lucide/svelte/icons/folder';
	import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import InfoTip from '$lib/components/info-tip.svelte';

	type App = { name: string; running: boolean; category: string; lastDeployOk: boolean; maintenance: boolean };
	type Service = { type: string; name: string; status: string; category: string; links: string[] };

	let apps = $state<App[]>([]);
	let services = $state<Service[]>([]);
	let projects = $state<string[]>([]);
	let loading = $state(true);

	let newProjOpen = $state(false);
	let newProjName = $state('');
	let creating = $state(false);

	const unassignedApps = $derived(apps.filter((a) => !a.category));
	const unassignedServices = $derived(services.filter((s) => !s.category));

	function projApps(p: string) {
		return apps.filter((a) => a.category === p);
	}
	function projServices(p: string) {
		return services.filter((s) => s.category === p);
	}

	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	async function load() {
		loading = true;
		try {
			const [a, s] = await Promise.all([api('/apps'), api('/services')]);
			apps = a.apps ?? [];
			services = s.services ?? [];
			// both endpoints return the shared project list plus any strays
			const seen = new Set<string>();
			projects = [...(a.categories ?? []), ...(s.categories ?? [])]
				.filter((c) => {
					if (!c || seen.has(c)) return false;
					seen.add(c);
					return true;
				})
				.sort((x, y) => x.localeCompare(y));
		} finally {
			loading = false;
		}
	}
	onMount(load);

	async function createProject(e: SubmitEvent) {
		e.preventDefault();
		creating = true;
		try {
			await api('/projects', { method: 'POST', body: JSON.stringify({ name: newProjName.trim() }) });
			toast.success(`Project "${newProjName.trim()}" created`);
			newProjOpen = false;
			newProjName = '';
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			creating = false;
		}
	}

	async function deleteProject(name: string) {
		const ok = await askConfirm(
			`Delete project "${name}"? Its apps and databases are kept and move to Unassigned. Their env vars stay as they are.`
		);
		if (!ok) return;
		try {
			await api('/projects', { method: 'DELETE', body: JSON.stringify({ name }) });
			toast.success(`Deleted "${name}"`);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	async function assignApp(app: string, project: string) {
		try {
			await api(`/apps/${app}/category`, { method: 'POST', body: JSON.stringify({ category: project }) });
			toast.success(`Moved ${app} to ${project}`);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	async function assignService(s: Service, project: string) {
		try {
			await api('/services/category', {
				method: 'POST',
				body: JSON.stringify({ type: s.type, name: s.name, category: project })
			});
			toast.success(`Moved ${s.name} to ${project}`);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}
</script>

<div class="mx-auto max-w-5xl">
	<div class="mb-6 flex flex-wrap items-center gap-2">
		<h1 class="flex items-center gap-1.5 text-2xl font-semibold tracking-tight">
			Projects
			<InfoTip
				text="A project groups the apps and databases that belong together, like a website and its API and database. Each project has shared environment variables that every app in it inherits."
			/>
		</h1>
		<div class="ml-auto flex flex-wrap gap-2">
			<Button variant="outline" size="sm" onclick={load} disabled={loading}>
				<RefreshCwIcon class="size-4 {loading ? 'animate-spin' : ''}" /> Refresh
			</Button>
			<Button size="sm" onclick={() => (newProjOpen = true)}>
				<FolderPlusIcon class="size-4" /> New project
			</Button>
		</div>
	</div>

	{#if loading && !projects.length && !apps.length}
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
			{#each Array(3) as _, i (i)}<Skeleton class="h-28" />{/each}
		</div>
	{:else}
		<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
			{#each projects as p (p)}
				{@const pApps = projApps(p)}
				{@const pSvcs = projServices(p)}
				{@const runningCount = pApps.filter((a) => a.running).length}
				{@const members = [...pApps.map((a) => a.name), ...pSvcs.map((s) => s.name)]}
				<a href="/project/{encodeURIComponent(p)}" class="group block h-full">
					<Card.Root class="group-hover:border-primary/50 h-full transition-colors">
						<Card.Header>
							<Card.Title class="flex items-center gap-2 text-base">
								<FolderIcon class="text-muted-foreground size-4 shrink-0" />
								<span class="truncate">{p}</span>
								{#if pApps.length}
									<span
										class="ml-auto size-2 shrink-0 rounded-full {runningCount === pApps.length
											? 'bg-emerald-500'
											: runningCount
												? 'bg-amber-500'
												: 'bg-red-500'}"
										title="{runningCount} of {pApps.length} apps running"
									></span>
								{/if}
								<button
									class="text-muted-foreground hover:text-destructive shrink-0 opacity-0 transition-opacity group-hover:opacity-100 {pApps.length
										? ''
										: 'ml-auto'}"
									onclick={(e) => {
										e.preventDefault();
										deleteProject(p);
									}}
									title="Delete project"
									aria-label="Delete project {p}"
								>
									<Trash2Icon class="size-4" />
								</button>
							</Card.Title>
							<Card.Description>
								{pApps.length}
								{pApps.length === 1 ? 'app' : 'apps'} · {pSvcs.length}
								{pSvcs.length === 1 ? 'database' : 'databases'}
							</Card.Description>
						</Card.Header>
						{#if members.length}
							<Card.Content class="flex flex-wrap gap-1.5">
								{#each members.slice(0, 5) as m (m)}
									<span class="bg-muted/60 text-muted-foreground rounded px-1.5 py-0.5 font-mono text-xs">{m}</span>
								{/each}
								{#if members.length > 5}
									<span class="text-muted-foreground px-1 py-0.5 text-xs">+{members.length - 5} more</span>
								{/if}
							</Card.Content>
						{/if}
					</Card.Root>
				</a>
			{:else}
				<p class="text-muted-foreground text-sm sm:col-span-2 lg:col-span-3">
					No projects yet. Create one, then add apps and databases to it.
				</p>
			{/each}
		</div>

		{#if unassignedApps.length || unassignedServices.length}
			<h2 class="text-muted-foreground mt-10 mb-2 text-xs font-medium tracking-widest uppercase">
				Unassigned
			</h2>
			<div class="divide-border overflow-hidden rounded-lg border divide-y">
				{#each unassignedApps as a (a.name)}
					<div class="bg-muted/30 flex items-center gap-2 px-3 py-2">
						<BoxIcon class="text-muted-foreground size-4 shrink-0" />
						<a href="/app/{a.name}" class="truncate text-sm hover:underline">{a.name}</a>
						<span
							class="size-2 shrink-0 rounded-full {a.running ? 'bg-emerald-500' : 'bg-red-500'}"
							title={a.running ? 'running' : 'stopped'}
						></span>
						{#if projects.length}
							<select
								class="border-input text-muted-foreground ml-auto h-7 rounded-md border bg-transparent px-2 text-xs"
								value=""
								onchange={(e) => {
									const p = (e.target as HTMLSelectElement).value;
									if (p) assignApp(a.name, p);
								}}
							>
								<option value="">Add to project…</option>
								{#each projects as p (p)}<option value={p}>{p}</option>{/each}
							</select>
						{/if}
					</div>
				{/each}
				{#each unassignedServices as s (s.type + s.name)}
					<div class="bg-muted/30 flex items-center gap-2 px-3 py-2">
						<DatabaseIcon class="text-muted-foreground size-4 shrink-0" />
						<span class="truncate text-sm">{s.name}</span>
						<span class="text-muted-foreground text-xs">{s.type}</span>
						{#if projects.length}
							<select
								class="border-input text-muted-foreground ml-auto h-7 rounded-md border bg-transparent px-2 text-xs"
								value=""
								onchange={(e) => {
									const p = (e.target as HTMLSelectElement).value;
									if (p) assignService(s, p);
								}}
							>
								<option value="">Add to project…</option>
								{#each projects as p (p)}<option value={p}>{p}</option>{/each}
							</select>
						{/if}
					</div>
				{/each}
			</div>
			<p class="text-muted-foreground mt-2 text-xs">
				Apps and databases that aren't in any project yet. Pick a project to move them in.
			</p>
		{/if}
	{/if}
</div>

<Dialog.Root bind:open={newProjOpen}>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title>New project</Dialog.Title>
			<Dialog.Description>
				Groups apps and databases that belong together and gives them shared environment variables.
			</Dialog.Description>
		</Dialog.Header>
		<form onsubmit={createProject} class="grid gap-4">
			<Input bind:value={newProjName} required placeholder="e.g. Client site" />
			<Button type="submit" disabled={creating}>{creating ? 'Creating…' : 'Create project'}</Button>
		</form>
	</Dialog.Content>
</Dialog.Root>
