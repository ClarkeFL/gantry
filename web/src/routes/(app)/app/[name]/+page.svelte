<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, stream } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Card from '$lib/components/ui/card';
	import * as Tabs from '$lib/components/ui/tabs';
	import * as Dialog from '$lib/components/ui/dialog';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
	import SquareIcon from '@lucide/svelte/icons/square';
	import PlayIcon from '@lucide/svelte/icons/play';
	import RocketIcon from '@lucide/svelte/icons/rocket';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import LockIcon from '@lucide/svelte/icons/lock';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';

	type Job = { id: string; schedule: string; command: string; last?: string };
	type Domain = { name: string; dnsOk: boolean };
	type Detail = {
		name: string;
		running: boolean;
		category: string;
		env: Record<string, string>;
		domains: Domain[];
		ssl: boolean;
		leEmailSet: boolean;
		jobs: Job[];
		nativeCron: string;
		repo: string;
		ref: string;
		buildDir: string;
		dockerfile: string;
		image: string;
	};

	const name = $derived(page.params.name!);

	let d = $state<Detail | null>(null);
	let tab = $state('overview');

	// env editor
	let rows = $state<{ key: string; value: string }[]>([]);
	let origEnv: Record<string, string> = {};
	let restartAfterSave = $state(true);
	let savingEnv = $state(false);

	// cron editor
	let jobs = $state<Job[]>([]);
	let savingCron = $state(false);

	// category
	let category = $state('');
	let savingCategory = $state(false);

	// logs
	let logLines = $state<string[]>([]);
	let logEl = $state<HTMLElement | null>(null);
	let stopLogs: (() => void) | null = null;

	// deploy
	let deployOpen = $state(false);
	let deploying = $state(false);
	let deployLines = $state<string[]>([]);
	let stopDeploy: (() => void) | null = null;

	// source config
	let srcType = $state('none');
	let srcRepo = $state('');
	let srcRef = $state('');
	let srcBuildDir = $state('');
	let srcDf = $state('');
	let srcImage = $state('');
	let savingSrc = $state(false);
	const sourceSummary = $derived(
		srcType === 'repo' && srcRepo
			? `${srcRepo}${srcRef ? ' @ ' + srcRef : ''}${srcBuildDir ? ' (' + srcBuildDir + ')' : ''}`
			: srcType === 'image' && srcImage
				? srcImage
				: 'last deployed source (rebuild)'
	);

	// domains + ssl
	let newDomain = $state('');
	let sslOpen = $state(false);
	let sslRunning = $state(false);
	let sslLines = $state<string[]>([]);
	let stopSSL: (() => void) | null = null;
	let autoSSLTried = false; // once per page visit — avoids hammering LE rate limits on failures
	let dnsTimer: ReturnType<typeof setInterval> | undefined;

	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	async function load() {
		d = await api<Detail>('/apps/' + name);
		rows = Object.entries(d.env).map(([key, value]) => ({ key, value }));
		origEnv = { ...d.env };
		jobs = d.jobs.map((j) => ({ ...j }));
		category = d.category;
		srcRepo = d.repo ?? '';
		srcRef = d.ref ?? '';
		srcBuildDir = d.buildDir ?? '';
		srcDf = d.dockerfile ?? '';
		srcImage = d.image ?? '';
		srcType = srcRepo ? 'repo' : srcImage ? 'image' : 'none';

		const waiting = d.domains.some((x) => !x.dnsOk);
		// poll while any domain waits on DNS; when all resolve, request the cert once
		if (waiting && !dnsTimer) dnsTimer = setInterval(load, 30_000);
		if (!waiting && dnsTimer) {
			clearInterval(dnsTimer);
			dnsTimer = undefined;
		}
		if (!d.ssl && d.leEmailSet && d.domains.length && !waiting && !autoSSLTried && !sslRunning) {
			autoSSLTried = true;
			toast.info('All domains point here — requesting certificate');
			sslOpen = true;
			enableSSL();
		}
	}
	onMount(() => {
		load().catch((e) => toast.error(msg(e)));
	});
	onDestroy(() => {
		stopLogs?.();
		stopDeploy?.();
		stopSSL?.();
		if (dnsTimer) clearInterval(dnsTimer);
	});

	$effect(() => {
		if (tab === 'logs' && !stopLogs) {
			logLines = [];
			stopLogs = stream(`/apps/${name}/logs`, (l) => {
				logLines.push(l);
			});
		} else if (tab !== 'logs' && stopLogs) {
			stopLogs();
			stopLogs = null;
		}
	});
	$effect(() => {
		if (logLines.length && logEl) logEl.scrollTop = logEl.scrollHeight;
	});

	async function ps(action: 'restart' | 'stop' | 'start') {
		try {
			await api(`/apps/${name}/ps`, { method: 'POST', body: JSON.stringify({ action }) });
			toast.success(`${action}: ok`);
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	function startDeploy() {
		if (srcType === 'none' && !d?.running) {
			tab = 'source';
			toast.info('Set a deploy source first');
			return;
		}
		deployOpen = true;
		deploying = true;
		deployLines = [];
		// empty body → the server deploys from the saved source, with pre-flight checks
		stopDeploy = stream(
			`/apps/${name}/deploy`,
			(l) => {
				deployLines.push(l);
			},
			{ method: 'POST', body: '{}' },
			() => {
				deploying = false;
				load();
			}
		);
	}

	async function saveSource(andDeploy = false) {
		savingSrc = true;
		try {
			await api(`/apps/${name}/source`, {
				method: 'PUT',
				body: JSON.stringify({
					repo: srcType === 'repo' ? srcRepo : '',
					ref: srcType === 'repo' ? srcRef : '',
					buildDir: srcType === 'repo' ? srcBuildDir : '',
					dockerfile: srcType === 'repo' ? srcDf : '',
					image: srcType === 'image' ? srcImage : ''
				})
			});
			toast.success('Source saved');
			if (andDeploy) startDeploy();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingSrc = false;
		}
	}

	async function modDomain(action: 'add' | 'remove', domain: string) {
		try {
			const res = await api(`/apps/${name}/domains`, {
				method: 'POST',
				body: JSON.stringify({ action, domain })
			});
			newDomain = '';
			await load();
			if (action === 'remove') {
				toast.success(`Removed ${domain}`);
			} else if (res.dnsOk) {
				toast.success(`Added ${domain} — requesting certificate`);
				sslOpen = true;
				enableSSL();
			} else {
				toast.info(`Added ${domain}. Its DNS doesn't point at this server yet — HTTPS will be one click away once it does.`, { duration: 8000 });
			}
		} catch (e) {
			toast.error(msg(e));
		}
	}

	function enableSSL() {
		sslRunning = true;
		sslLines = [];
		stopSSL = stream(
			`/apps/${name}/ssl`,
			(l) => {
				sslLines.push(l);
			},
			{ method: 'POST' },
			() => {
				sslRunning = false;
				load();
			}
		);
	}

	function importEnvFile(e: Event) {
		const file = (e.target as HTMLInputElement).files?.[0];
		if (!file) return;
		file.text().then((text) => {
			let count = 0;
			for (let line of text.split('\n')) {
				line = line.trim();
				if (!line || line.startsWith('#')) continue;
				line = line.replace(/^export\s+/, '');
				const eq = line.indexOf('=');
				if (eq < 1) continue;
				const key = line.slice(0, eq).trim();
				let value = line.slice(eq + 1).trim();
				if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
					value = value.slice(1, -1);
				}
				const existing = rows.find((r) => r.key === key);
				if (existing) existing.value = value;
				else rows.push({ key, value });
				count++;
			}
			toast.success(`Imported ${count} variables — review below, then Save changes`);
		});
		(e.target as HTMLInputElement).value = '';
	}

	async function saveEnv() {
		const set: Record<string, string> = {};
		const seen = new Set<string>();
		for (const r of rows) {
			const k = r.key.trim();
			if (!k) continue;
			if (seen.has(k)) {
				toast.error('Duplicate key: ' + k);
				return;
			}
			seen.add(k);
			if (origEnv[k] !== r.value) set[k] = r.value;
		}
		const unset = Object.keys(origEnv).filter((k) => !seen.has(k));
		if (!Object.keys(set).length && !unset.length) {
			toast.info('Nothing changed');
			return;
		}
		savingEnv = true;
		try {
			await api(`/apps/${name}/env`, {
				method: 'POST',
				body: JSON.stringify({ set, unset, restart: restartAfterSave })
			});
			toast.success('Environment saved' + (restartAfterSave ? ' — app restarting' : ''));
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingEnv = false;
		}
	}

	async function saveCron() {
		savingCron = true;
		try {
			const res = await api<{ jobs: Job[] }>(`/apps/${name}/cron`, {
				method: 'PUT',
				body: JSON.stringify({ jobs: jobs.map(({ id, schedule, command }) => ({ id, schedule, command })) })
			});
			jobs = res.jobs;
			toast.success('Cron jobs saved');
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingCron = false;
		}
	}

	async function saveCategory() {
		savingCategory = true;
		try {
			await api(`/apps/${name}/category`, { method: 'POST', body: JSON.stringify({ category }) });
			toast.success('Category saved');
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingCategory = false;
		}
	}

	function lastBadge(last?: string): { label: string; ok: boolean } | null {
		if (!last) return null;
		const [ts] = last.split(' ');
		return { label: `${last.includes('exit=0') ? 'ok' : 'failed'} · ${ts.replace('T', ' ').slice(0, 16)}`, ok: last.includes('exit=0') };
	}
</script>

<div class="mx-auto max-w-4xl">
	<a href="/apps" class="text-muted-foreground hover:text-foreground mb-4 inline-flex items-center gap-1 text-sm">
		<ArrowLeftIcon class="size-4" /> Apps
	</a>

	{#if !d}
		<Skeleton class="h-64" />
	{:else}
		<div class="mb-6 flex flex-wrap items-center gap-3">
			<h1 class="text-2xl font-semibold tracking-tight">{d.name}</h1>
			<Badge variant={d.running ? 'default' : 'destructive'}>{d.running ? 'running' : 'stopped'}</Badge>
			<div class="ml-auto flex gap-2">
				{#if d.running}
					<Button variant="outline" size="sm" onclick={() => ps('restart')}>
						<RotateCwIcon class="size-4" /> Restart
					</Button>
					<Button variant="outline" size="sm" onclick={() => ps('stop')}>
						<SquareIcon class="size-4" /> Stop
					</Button>
				{:else}
					<Button variant="outline" size="sm" onclick={() => ps('start')}>
						<PlayIcon class="size-4" /> Start
					</Button>
				{/if}
				<Button size="sm" onclick={startDeploy}>
					<RocketIcon class="size-4" /> Deploy
				</Button>
			</div>
		</div>

		<Tabs.Root bind:value={tab}>
			<Tabs.List>
				<Tabs.Trigger value="overview">Overview</Tabs.Trigger>
				<Tabs.Trigger value="source">Source</Tabs.Trigger>
				<Tabs.Trigger value="env">Environment</Tabs.Trigger>
				<Tabs.Trigger value="cron">Cron</Tabs.Trigger>
				<Tabs.Trigger value="logs">Logs</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="source" class="mt-4">
				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Deploy source</Card.Title>
						<Card.Description>
							Where this app deploys from. Build method is auto-detected: a Dockerfile in the repo
							uses <code>docker build</code>, otherwise buildpacks.
						</Card.Description>
					</Card.Header>
					<Card.Content class="grid max-w-xl gap-4">
						<Tabs.Root bind:value={srcType}>
							<Tabs.List>
								<Tabs.Trigger value="repo">GitHub repo</Tabs.Trigger>
								<Tabs.Trigger value="image">Docker image</Tabs.Trigger>
								<Tabs.Trigger value="none">None</Tabs.Trigger>
							</Tabs.List>
						</Tabs.Root>
						{#if srcType === 'none'}
							<p class="text-muted-foreground text-sm">
								No managed source — deploy via <code>git push dokku</code> or the CLI. The Deploy
								button falls back to rebuilding the last deployed code.
							</p>
						{/if}
						{#if srcType === 'repo'}
							<div class="grid gap-2">
								<Label for="src-repo">Repository URL</Label>
								<Input id="src-repo" class="font-mono text-xs" placeholder="https://github.com/you/repo" bind:value={srcRepo} />
							</div>
							<div class="grid grid-cols-2 gap-3">
								<div class="grid gap-2">
									<Label for="src-ref">Branch</Label>
									<Input id="src-ref" class="font-mono text-xs" placeholder="main" bind:value={srcRef} />
								</div>
								<div class="grid gap-2">
									<Label for="src-bd">Build path <span class="text-muted-foreground">(monorepo)</span></Label>
									<Input id="src-bd" class="font-mono text-xs" placeholder="apps/web" bind:value={srcBuildDir} />
								</div>
							</div>
							<div class="grid gap-2">
								<Label for="src-df">Dockerfile path <span class="text-muted-foreground">(optional, relative to build path)</span></Label>
								<Input id="src-df" class="font-mono text-xs" placeholder="Dockerfile" bind:value={srcDf} />
							</div>
							<p class="text-muted-foreground text-xs">
								Private repo? Save your GitHub username + token in Settings — deploys authenticate with it.
							</p>
						{:else if srcType === 'image'}
							<div class="grid gap-2">
								<Label for="src-img">Image</Label>
								<Input id="src-img" class="font-mono text-xs" placeholder="ghcr.io/you/app:latest" bind:value={srcImage} />
							</div>
						{/if}
						<div class="flex gap-2">
							<Button variant="outline" onclick={() => saveSource(false)} disabled={savingSrc}>Save</Button>
							{#if srcType !== 'none'}
								<Button onclick={() => saveSource(true)} disabled={savingSrc}>
									<RocketIcon class="size-4" /> Save & deploy
								</Button>
							{/if}
						</div>
					</Card.Content>
				</Card.Root>
			</Tabs.Content>

			<Tabs.Content value="overview" class="mt-4 grid gap-4">
				<Card.Root>
					<Card.Header>
						<Card.Title class="flex items-center gap-2 text-base">
							Domains
							{#if d.ssl}
								<Badge variant="secondary" class="gap-1"><LockIcon class="size-3" /> https</Badge>
							{/if}
						</Card.Title>
						<Card.Description>
							Point the domain's DNS at this server first, then enable HTTPS for a Let's Encrypt certificate.
						</Card.Description>
					</Card.Header>
					<Card.Content>
						<div class="divide-border overflow-hidden rounded-lg border divide-y">
							{#each d.domains as domain (domain.name)}
								<div class="bg-muted/30 flex items-center gap-1 px-3 py-2">
									<span class="text-muted-foreground font-mono text-xs">{d.ssl ? 'https://' : 'http://'}</span>
									<span class="flex-1 truncate font-mono text-xs">{domain.name}</span>
									{#if d.ssl && domain.dnsOk}
										<LockIcon class="size-3.5 text-emerald-500" />
									{:else if !domain.dnsOk}
										<span class="flex items-center gap-1.5 text-xs text-amber-500" title="Point this domain's DNS at the server IP — checked every 30s">
											<span class="size-1.5 animate-pulse rounded-full bg-amber-500"></span>
											waiting for DNS
										</span>
									{/if}
									<a
										href="{d.ssl ? 'https' : 'http'}://{domain.name}"
										target="_blank"
										rel="noreferrer"
										class="text-muted-foreground hover:text-foreground p-1.5"
										aria-label="Open {domain.name}"
									>
										<ExternalLinkIcon class="size-4" />
									</a>
									<button
										class="text-muted-foreground hover:text-destructive p-1.5"
										onclick={() => modDomain('remove', domain.name)}
										aria-label="Remove {domain.name}"
									>
										<Trash2Icon class="size-4" />
									</button>
								</div>
							{:else}
								<p class="text-muted-foreground px-3 py-2 text-sm">No domains configured.</p>
							{/each}
							<div class="flex items-center gap-2 px-3 py-2">
								<Input
									bind:value={newDomain}
									placeholder="app.example.com"
									class="h-8 flex-1 border-0 bg-transparent! font-mono text-xs shadow-none focus-visible:ring-0"
									onkeydown={(e) => e.key === 'Enter' && newDomain.trim() && modDomain('add', newDomain.trim())}
								/>
								<Button
									variant="ghost"
									size="sm"
									disabled={!newDomain.trim()}
									onclick={() => modDomain('add', newDomain.trim())}
								>
									<PlusIcon class="size-4" /> Add domain
								</Button>
							</div>
						</div>
						{#if d.domains.length && !d.ssl}
							<Button size="sm" class="mt-3" onclick={() => (sslOpen = true)}>
								<LockIcon class="size-4" /> Enable HTTPS
							</Button>
						{/if}
					</Card.Content>
				</Card.Root>

				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Category</Card.Title>
						<Card.Description>Groups this app on the dashboard.</Card.Description>
					</Card.Header>
					<Card.Content class="flex max-w-sm gap-2">
						<Input bind:value={category} placeholder="e.g. Websites, Backends, Clients…" />
						<Button variant="outline" onclick={saveCategory} disabled={savingCategory}>Save</Button>
					</Card.Content>
				</Card.Root>

				{#if d.nativeCron}
					<Card.Root>
						<Card.Header>
							<Card.Title class="text-base">app.json cron (dokku-native)</Card.Title>
						</Card.Header>
						<Card.Content>
							<pre class="text-muted-foreground overflow-x-auto text-xs">{d.nativeCron}</pre>
						</Card.Content>
					</Card.Root>
				{/if}
			</Tabs.Content>

			<Tabs.Content value="env" class="mt-4">
				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Environment variables</Card.Title>
						<Card.Description>Saved with <code>--no-restart</code>; toggle below to restart after saving.</Card.Description>
					</Card.Header>
					<Card.Content class="grid gap-2">
						{#each rows as row, i (i)}
							<div class="flex gap-2">
								<Input class="w-56 font-mono text-xs" placeholder="KEY" bind:value={row.key} />
								<Input class="flex-1 font-mono text-xs" placeholder="value" bind:value={row.value} />
								<Button
									variant="ghost"
									size="icon"
									onclick={() => (rows = rows.filter((_, j) => j !== i))}
									aria-label="Remove variable"
								>
									<Trash2Icon class="size-4" />
								</Button>
							</div>
						{:else}
							<p class="text-muted-foreground text-sm">No variables set.</p>
						{/each}
						<div class="mt-2 flex items-center gap-2">
							<Button variant="outline" size="sm" onclick={() => rows.push({ key: '', value: '' })}>
								<PlusIcon class="size-4" /> Add variable
							</Button>
							<Button variant="outline" size="sm" onclick={() => document.getElementById('env-file')?.click()}>
								<UploadIcon class="size-4" /> Import .env
							</Button>
							<input id="env-file" type="file" accept=".env,text/plain,.txt" class="hidden" onchange={importEnvFile} />
							<div class="ml-auto flex items-center gap-2">
								<Switch id="restart" bind:checked={restartAfterSave} />
								<Label for="restart" class="text-sm">Restart app</Label>
							</div>
							<Button size="sm" onclick={saveEnv} disabled={savingEnv}>
								{savingEnv ? 'Saving…' : 'Save changes'}
							</Button>
						</div>
					</Card.Content>
				</Card.Root>
			</Tabs.Content>

			<Tabs.Content value="cron" class="mt-4">
				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Scheduled jobs</Card.Title>
						<Card.Description>
							Each job runs in a fresh one-off container of this app (<code>dokku --rm run {d.name} …</code>) —
							0MB between runs. Editable live, no redeploy.
						</Card.Description>
					</Card.Header>
					<Card.Content class="grid gap-2">
						{#each jobs as job, i (job.id || i)}
							{@const badge = lastBadge(job.last)}
							<div class="flex items-center gap-2">
								<Input class="w-36 font-mono text-xs" placeholder="0 3 * * *" bind:value={job.schedule} />
								<Input class="flex-1 font-mono text-xs" placeholder="node scripts/cleanup.js" bind:value={job.command} />
								{#if badge}
									<Badge variant={badge.ok ? 'secondary' : 'destructive'} class="whitespace-nowrap">
										{badge.label}
									</Badge>
								{/if}
								<Button
									variant="ghost"
									size="icon"
									onclick={() => (jobs = jobs.filter((_, j) => j !== i))}
									aria-label="Remove job"
								>
									<Trash2Icon class="size-4" />
								</Button>
							</div>
						{:else}
							<p class="text-muted-foreground text-sm">No jobs yet. Schedules use standard cron syntax or @daily / @hourly.</p>
						{/each}
						<div class="mt-2 flex items-center gap-2">
							<Button
								variant="outline"
								size="sm"
								onclick={() => jobs.push({ id: '', schedule: '', command: '' })}
							>
								<PlusIcon class="size-4" /> Add job
							</Button>
							<Button size="sm" class="ml-auto" onclick={saveCron} disabled={savingCron}>
								{savingCron ? 'Saving…' : 'Save jobs'}
							</Button>
						</div>
					</Card.Content>
				</Card.Root>
			</Tabs.Content>

			<Tabs.Content value="logs" class="mt-4">
				<div
					bind:this={logEl}
					class="bg-card h-[32rem] overflow-y-auto rounded-lg border p-4 font-mono text-xs leading-5"
				>
					{#each logLines as line, i (i)}
						<div class="whitespace-pre-wrap">{line}</div>
					{:else}
						<p class="text-muted-foreground">Waiting for logs…</p>
					{/each}
				</div>
			</Tabs.Content>
		</Tabs.Root>
	{/if}
</div>

<Dialog.Root bind:open={sslOpen}>
	<Dialog.Content class="max-w-xl">
		<Dialog.Header>
			<Dialog.Title>Enable HTTPS for {name}</Dialog.Title>
			<Dialog.Description>
				Requests a Let's Encrypt certificate for all domains on this app and sets up auto-renewal.
				The domains must already resolve to this server or issuance fails.
			</Dialog.Description>
		</Dialog.Header>
		<Button onclick={enableSSL} disabled={sslRunning} class="w-fit">
			<LockIcon class="size-4" />
			{sslRunning ? 'Requesting certificate…' : 'Request certificate'}
		</Button>
		{#if sslLines.length}
			<div class="bg-card mt-2 max-h-64 overflow-y-auto rounded-md border p-3 font-mono text-xs leading-5">
				{#each sslLines as line, i (i)}<div class="whitespace-pre-wrap">{line}</div>{/each}
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<Dialog.Root bind:open={deployOpen}>
	<Dialog.Content class="max-w-2xl">
		<Dialog.Header>
			<Dialog.Title>{deploying ? 'Deploying' : 'Deployed'} {name}</Dialog.Title>
			<Dialog.Description>
				Source: <code class="text-foreground">{sourceSummary}</code> — change it on the Source tab.
			</Dialog.Description>
		</Dialog.Header>
		{#if deployLines.length}
			<div class="bg-card mt-2 h-64 overflow-y-auto rounded-md border p-3 font-mono text-xs leading-5">
				{#each deployLines as line, i (i)}
					<div class="whitespace-pre-wrap">{line}</div>
				{/each}
			</div>
		{/if}
	</Dialog.Content>
</Dialog.Root>
