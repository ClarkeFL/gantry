<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, stream } from '$lib/api';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import CronInput from '$lib/components/cron-input.svelte';
	import EnvEditor from '$lib/components/env-editor.svelte';
	import { ago, fmtDate } from '$lib/dates';
	import { userTzFull, serverTzLabel } from '$lib/server-info.svelte';
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
	import WrenchIcon from '@lucide/svelte/icons/wrench';
	import CloudIcon from '@lucide/svelte/icons/cloud';
	import HardDriveIcon from '@lucide/svelte/icons/hard-drive';
	import ScrollTextIcon from '@lucide/svelte/icons/scroll-text';
	import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
	import CheckIcon from '@lucide/svelte/icons/check';
	import XIcon from '@lucide/svelte/icons/x';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import { askConfirm } from '$lib/confirm.svelte';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';

	type Job = { id: string; schedule: string; command: string; disabled?: boolean; last?: string };
	type Domain = { name: string; dnsOk: boolean; proxied?: boolean };
	type Detail = {
		name: string;
		running: boolean;
		category: string;
		env: Record<string, string>;
		projectEnv: Record<string, string>;
		projects: string[];
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
		lastDeploy: string;
		lastDeployOk: boolean;
		maintenance: boolean;
		maintenanceTpl: string;
		mounts: { hostDir: string; containerPath: string }[];
	};

	const name = $derived(page.params.name!);

	let d = $state<Detail | null>(null);
	let tab = $state(page.url.searchParams.get('tab') ?? 'overview');

	// env editor. Dokku's own bookkeeping vars (DOKKU_*, GIT_REV) are shown
	// read-only and excluded from the editable rows AND the unset diff, so a
	// save can never delete them and break the app.
	const isSysEnv = (k: string) => k.startsWith('DOKKU_') || k === 'GIT_REV';
	let editEnv = $state<Record<string, string>>({});
	let sysEnv = $state<[string, string][]>([]);

	// cron editor
	let jobs = $state<Job[]>([]);
	let savingCron = $state(false);

	// category
	let category = $state('');
	let savingCategory = $state(false);

	// destroy
	let destroyConfirm = $state('');
	let destroying = $state(false);

	// maintenance mode
	const MAINT_TEMPLATES = [
		{ id: 'clean', label: 'Clean', desc: 'Light page, "We’ll be right back".' },
		{ id: 'dark', label: 'Dark', desc: 'Dark page with a loading spinner.' },
		{ id: 'construction', label: 'Construction', desc: 'Playful "under construction" page.' }
	];
	let maintTpl = $state('clean');
	let maintBusy = $state(false);

	// persistent storage
	let newMount = $state('');
	let mountBusy = $state(false);

	// deploy history
	type DeployEntry = { id: string; started: string; finished?: string; source?: string; status: string };
	let deploys = $state<DeployEntry[]>([]);
	let deploysLoading = $state(false);
	let histLogOpen = $state(false);
	let histLogText = $state('');
	let histLog = $state<DeployEntry | null>(null);

	// logs: stored history merged with the live tail in one view
	type HistLine = { t: number; line: string; sev?: string };
	const HIST_BUCKETS = 48;
	let logEl = $state<HTMLElement | null>(null);
	let stopLogs: (() => void) | null = null;
	let logAtBottom = $state(true); // follow the tail unless the user scrolled up
	let histLines = $state<HistLine[]>([]);
	let histLoading = $state(false);
	let histHours = $state(24);
	let histLoadedAt = $state(Date.now());
	let histFilter = $state('');
	let histSev = $state('');
	let histBucket = $state(-1);
	let histRetention = $state(7);

	// severity heuristics, mirrors the server-side classifier for live lines
	const jsErrRe = /\b(error|exception|fatal|panic|traceback)\b|\s5\d\d\s/i;
	const jsWarnRe = /\bwarn(ing)?\b|\s4\d\d\s/i;
	function classifyLive(line: string): string {
		return jsErrRe.test(line) ? 'e' : jsWarnRe.test(line) ? 'w' : '';
	}

	const histRange = $derived.by(() => {
		// the end extends as live lines arrive so the last bar keeps filling
		const end = Math.max(histLoadedAt, histLines[histLines.length - 1]?.t ?? 0);
		const start = histLoadedAt - histHours * 3600_000;
		return { start, end, span: Math.max(1, (end - start) / HIST_BUCKETS) };
	});
	const histBuckets = $derived.by(() => {
		const b = Array.from({ length: HIST_BUCKETS }, () => ({ total: 0, w: 0, e: 0 }));
		for (const l of histLines) {
			const i = Math.min(HIST_BUCKETS - 1, Math.max(0, Math.floor((l.t - histRange.start) / histRange.span)));
			b[i].total++;
			if (l.sev === 'w') b[i].w++;
			else if (l.sev === 'e') b[i].e++;
		}
		return b;
	});
	const histMax = $derived(Math.max(1, ...histBuckets.map((b) => b.total)));
	const visibleHist = $derived.by(() => {
		let out = histLines;
		if (histBucket >= 0) {
			const s = histRange.start + histBucket * histRange.span;
			out = out.filter((l) => l.t >= s && l.t < s + histRange.span);
		}
		if (histSev) out = out.filter((l) => l.sev === histSev);
		const q = histFilter.trim().toLowerCase();
		if (q) out = out.filter((l) => l.line.toLowerCase().includes(q));
		return out.slice(-500);
	});

	async function loadHistory() {
		histLoading = true;
		histBucket = -1;
		try {
			const d = await api(`/apps/${name}/logs/history?hours=${histHours}`);
			histLines = d.lines ?? [];
			histRetention = d.retentionDays ?? 7;
			histLoadedAt = Date.now();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			histLoading = false;
		}
	}

	function histTime(t: number, withDate = histHours > 24) {
		const d = new Date(t);
		const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
		if (!withDate) return time;
		return d.toLocaleDateString(undefined, { day: '2-digit', month: '2-digit' }) + ' ' + time;
	}

	// live deploy (shown as a card on the Deploys tab — no modal)
	type LiveDeploy = {
		started: string;
		source: string;
		status: 'running' | 'success' | 'failed';
		lines: string[];
	};
	let liveDeploy = $state<LiveDeploy | null>(null);
	let deploying = $state(false);
	let deployLogOpen = $state(true);
	let deployLogEl = $state<HTMLElement | null>(null);
	let stopDeploy: (() => void) | null = null;

	// source config
	let srcType = $state('none');
	let srcRepo = $state('');
	let srcRef = $state('');
	let srcBuildDir = $state('');
	let srcDf = $state('');
	let srcImage = $state('');
	let imgUser = $state('');
	let imgPass = $state('');
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
	let autoSSLTried = false; // once per page visit, avoids hammering LE rate limits on failures
	let dnsTimer: ReturnType<typeof setInterval> | undefined;

	function msg(e: unknown) {
		return e instanceof Error ? e.message : String(e);
	}

	async function load() {
		d = await api<Detail>('/apps/' + name);
		const entries = Object.entries(d.env);
		sysEnv = entries.filter(([k]) => isSysEnv(k)).sort((a, b) => a[0].localeCompare(b[0]));
		editEnv = Object.fromEntries(entries.filter(([k]) => !isSysEnv(k)));
		jobs = d.jobs.map((j) => ({ ...j }));
		category = d.category;
		srcRepo = d.repo ?? '';
		srcRef = d.ref ?? '';
		srcBuildDir = d.buildDir ?? '';
		srcDf = d.dockerfile ?? '';
		srcImage = d.image ?? '';
		srcType = srcRepo ? 'repo' : srcImage ? 'image' : 'none';
		maintTpl = d.maintenanceTpl || 'clean';

		const waiting = d.domains.some((x) => !x.dnsOk);
		// poll while any domain waits on DNS; when all resolve, request the cert once
		if (waiting && !dnsTimer) dnsTimer = setInterval(load, 30_000);
		if (!waiting && dnsTimer) {
			clearInterval(dnsTimer);
			dnsTimer = undefined;
		}
		if (!d.ssl && d.leEmailSet && d.domains.length && !waiting && !autoSSLTried && !sslRunning) {
			autoSSLTried = true;
			toast.info('All domains point here, requesting certificate');
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
			loadHistory().then(startLogTail);
		} else if (tab !== 'logs' && stopLogs) {
			stopLogs();
			stopLogs = null;
		}
	});
	$effect(() => {
		void visibleHist.length;
		if (logEl && logAtBottom) logEl.scrollTop = logEl.scrollHeight;
	});

	async function changeRange() {
		stopLogs?.();
		stopLogs = null;
		await loadHistory();
		startLogTail();
	}

	function startLogTail() {
		if (stopLogs || tab !== 'logs') return;
		let lastT = histLines[histLines.length - 1]?.t ?? 0;
		stopLogs = stream(`/apps/${name}/logs`, (raw) => {
			const sp = raw.indexOf(' ');
			const parsed = sp > 0 ? Date.parse(raw.slice(0, sp)) : NaN;
			const t = isNaN(parsed) ? Date.now() : parsed;
			if (t <= lastT) return; // overlap with stored history or the tail's backfill
			lastT = t;
			const line = isNaN(parsed) ? raw : raw.slice(sp + 1);
			histLines.push({ t, line, sev: classifyLive(line) });
			if (histLines.length > 3000) histLines.shift();
		});
	}
	$effect(() => {
		if (liveDeploy?.lines.length && deployLogEl) {
			deployLogEl.scrollTop = deployLogEl.scrollHeight;
		}
	});
	async function loadDeploys() {
		deploysLoading = true;
		try {
			deploys = await api<DeployEntry[]>(`/apps/${name}/deploys`);
		} catch (e) {
			toast.error(msg(e));
		} finally {
			deploysLoading = false;
		}
	}
	async function viewDeployLog(e: DeployEntry) {
		histLog = e;
		histLogText = '';
		histLogOpen = true;
		histLogText = await fetch(`/api/apps/${name}/logs/deploy?id=${e.id}`).then((r) => r.text());
	}
	function deployDuration(e: DeployEntry): string {
		if (!e.finished || !e.started) return '';
		const s = (new Date(e.finished).getTime() - new Date(e.started).getTime()) / 1000;
		if (isNaN(s) || s < 0) return '';
		return s >= 60 ? `${Math.floor(s / 60)}m ${Math.round(s % 60)}s` : `${Math.round(s)}s`;
	}
	$effect(() => {
		if (tab === 'deploys') loadDeploys();
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
		if (deploying) return;
		if (srcType === 'none' && !d?.running) {
			tab = 'source';
			toast.info('Set a deploy source first');
			return;
		}
		tab = 'deploys';
		loadDeploys();
		deploying = true;
		deployLogOpen = true;
		liveDeploy = {
			started: new Date().toISOString(),
			source: sourceSummary,
			status: 'running',
			lines: []
		};
		// empty body → the server deploys from the saved source, with pre-flight checks
		stopDeploy = stream(
			`/apps/${name}/deploy`,
			(l) => {
				if (!liveDeploy) return;
				liveDeploy.lines.push(l);
				if (l.includes('[gantry] done')) liveDeploy.status = 'success';
				else if (l.includes('[gantry] aborted') || l.startsWith('[error]')) liveDeploy.status = 'failed';
			},
			{ method: 'POST', body: '{}' },
			() => {
				deploying = false;
				if (liveDeploy && liveDeploy.status === 'running') {
					// stream ended without a terminal marker
					const failed = liveDeploy.lines.some((l) => l.startsWith('[error]') || l.includes('aborted'));
					liveDeploy.status = failed ? 'failed' : 'success';
				}
				const ok = liveDeploy?.status === 'success';
				if (ok) toast.success('Deploy finished');
				else toast.error('Deploy failed');
				load();
				loadDeploys();
			}
		);
	}

	async function saveSource(andDeploy = false) {
		savingSrc = true;
		try {
			if (srcType === 'image' && imgUser.trim() && imgPass) {
				const seg = srcImage.split('/')[0];
				const server = seg.includes('.') || seg.includes(':') ? seg : 'docker.io';
				await api('/settings/registry', {
					method: 'POST',
					body: JSON.stringify({ server, user: imgUser.trim(), password: imgPass })
				});
				toast.success(`Logged in to ${server}`);
				imgUser = '';
				imgPass = '';
			}
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
				toast.success(`Added ${domain}, requesting certificate`);
				sslOpen = true;
				enableSSL();
			} else {
				toast.info(`Added ${domain}. Its DNS doesn't point at this server yet, HTTPS will be one click away once it does.`, { duration: 8000 });
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

	async function saveEnv(set: Record<string, string>, unset: string[], restart: boolean) {
		try {
			await api(`/apps/${name}/env`, {
				method: 'POST',
				body: JSON.stringify({ set, unset, restart })
			});
			toast.success('Environment saved' + (restart ? ', app restarting' : ''));
			await load();
		} catch (e) {
			toast.error(msg(e));
		}
	}

	async function saveCron() {
		savingCron = true;
		try {
			const res = await api<{ jobs: Job[] }>(`/apps/${name}/cron`, {
				method: 'PUT',
				body: JSON.stringify({
					jobs: jobs.map(({ id, schedule, command, disabled }) => ({ id, schedule, command, disabled }))
				})
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
			toast.success(category ? `Moved to ${category}, shared variables applied` : 'Removed from project');
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			savingCategory = false;
		}
	}

	async function setMaintenance(on: boolean) {
		maintBusy = true;
		try {
			await api(`/apps/${name}/maintenance`, {
				method: 'POST',
				body: JSON.stringify({ on, template: maintTpl })
			});
			toast.success(on ? 'Maintenance page is up' : 'Maintenance mode is off, your site is back');
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			maintBusy = false;
		}
	}

	async function modMount(path: string, remove: boolean) {
		if (remove) {
			const ok = await askConfirm(
				`Detach ${path}? The folder and everything in it stays on the server, it just stops being connected to the app. Attaching the same path again brings the data back.`
			);
			if (!ok) return;
		}
		mountBusy = true;
		try {
			const res = await api<{ restarted: boolean }>(`/apps/${name}/storage`, {
				method: 'POST',
				body: JSON.stringify({ path, remove })
			});
			toast.success(
				(remove ? 'Folder detached' : 'Folder attached') +
					(res.restarted ? ', app restarted to apply it' : ', applies on the next start')
			);
			newMount = '';
			await load();
		} catch (e) {
			toast.error(msg(e));
		} finally {
			mountBusy = false;
		}
	}

	async function destroyApp() {
		destroying = true;
		try {
			await api(`/apps/${name}`, { method: 'DELETE' });
			toast.success(`Destroyed ${name}`);
			goto('/projects');
		} catch (e) {
			toast.error(msg(e));
			destroying = false;
		}
	}

	function lastBadge(last?: string): { label: string; ok: boolean } | null {
		if (!last) return null;
		const [ts] = last.split(' ');
		return { label: `${last.includes('exit=0') ? 'ok' : 'failed'} · ${ago(ts)}`, ok: last.includes('exit=0') };
	}
</script>

<div class="mx-auto max-w-4xl">
	<a
		href={d?.category ? `/project/${encodeURIComponent(d.category)}` : '/projects'}
		class="text-muted-foreground hover:text-foreground mb-4 inline-flex items-center gap-1 text-sm"
	>
		<ArrowLeftIcon class="size-4" />
		{d?.category || 'Projects'}
	</a>

	{#if !d}
		<Skeleton class="h-64" />
	{:else}
		<div class="mb-6 flex flex-wrap items-center gap-3">
			<h1 class="text-2xl font-semibold tracking-tight">{d.name}</h1>
			<Badge variant={d.running ? 'default' : 'destructive'}>{d.running ? 'running' : 'stopped'}</Badge>
			{#if d.maintenance}
				<Badge class="border-transparent bg-amber-500/15 text-amber-500">maintenance</Badge>
			{/if}
			{#if d.lastDeploy && !d.lastDeployOk}
				<Badge variant="destructive" title={fmtDate(d.lastDeploy)}>last deploy failed · {ago(d.lastDeploy)}</Badge>
			{/if}
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
				<Button size="sm" onclick={startDeploy} disabled={deploying}>
					{#if deploying}
						<LoaderCircleIcon class="size-4 animate-spin" /> Deploying…
					{:else}
						<RocketIcon class="size-4" /> Deploy
					{/if}
				</Button>
			</div>
		</div>

		<Tabs.Root bind:value={tab}>
			<Tabs.List>
				<Tabs.Trigger value="overview">Overview</Tabs.Trigger>
				<Tabs.Trigger value="source">Source</Tabs.Trigger>
				<Tabs.Trigger value="deploys">Deploys</Tabs.Trigger>
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
								No managed source, deploy via <code>git push dokku</code> or the CLI. The Deploy
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
								Private repo? Save your GitHub username + token in Settings, deploys authenticate with it.
							</p>
						{:else if srcType === 'image'}
							<div class="grid gap-2">
								<Label for="src-img">Image</Label>
								<Input id="src-img" class="font-mono text-xs" placeholder="ghcr.io/you/app:latest or you/app:latest" bind:value={srcImage} />
							</div>
							<div class="grid grid-cols-2 gap-3">
								<div class="grid gap-2">
									<Label for="src-imguser">Username <span class="text-muted-foreground">(private image)</span></Label>
									<Input id="src-imguser" bind:value={imgUser} autocomplete="off" />
								</div>
								<div class="grid gap-2">
									<Label for="src-imgpass">Password / token</Label>
									<Input id="src-imgpass" type="password" bind:value={imgPass} autocomplete="new-password" />
								</div>
							</div>
							<p class="text-muted-foreground text-xs">
								Only needed for private images. Credentials go straight to <code>docker login</code> on the
								server on Save, the panel doesn't store them. Existing logins: Settings → Docker registries.
							</p>
						{/if}
						<div class="flex gap-2">
							<Button variant="outline" onclick={() => saveSource(false)} disabled={savingSrc}>Save</Button>
							{#if srcType !== 'none'}
								<Button onclick={() => saveSource(true)} disabled={savingSrc || deploying}>
									{#if deploying}
										<LoaderCircleIcon class="size-4 animate-spin" /> Deploying…
									{:else}
										<RocketIcon class="size-4" /> Save & deploy
									{/if}
								</Button>
							{/if}
						</div>
					</Card.Content>
				</Card.Root>
			</Tabs.Content>

			<Tabs.Content value="deploys" class="mt-4 grid gap-4">
				{#if liveDeploy}
					<div
						class="animate-in fade-in slide-in-from-top-2 overflow-hidden rounded-xl border duration-300
							{liveDeploy.status === 'running'
							? 'border-amber-500/40 bg-amber-500/5 shadow-[0_0_0_1px_rgba(245,158,11,0.08)]'
							: liveDeploy.status === 'success'
								? 'border-emerald-500/40 bg-emerald-500/5'
								: 'border-red-500/40 bg-red-500/5'}"
					>
						{#if liveDeploy.status === 'running'}
							<div class="h-0.5 w-full overflow-hidden bg-amber-500/15">
								<div class="bg-amber-500 h-full w-1/3 animate-[deploy-slide_1.4s_ease-in-out_infinite]"></div>
							</div>
						{/if}
						<div class="flex items-start gap-3 p-4">
							<div
								class="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full
									{liveDeploy.status === 'running'
									? 'bg-amber-500/15 text-amber-500'
									: liveDeploy.status === 'success'
										? 'bg-emerald-500/15 text-emerald-500'
										: 'bg-red-500/15 text-red-500'}"
							>
								{#if liveDeploy.status === 'running'}
									<LoaderCircleIcon class="size-4 animate-spin" />
								{:else if liveDeploy.status === 'success'}
									<CheckIcon class="size-4" />
								{:else}
									<XIcon class="size-4" />
								{/if}
							</div>
							<div class="min-w-0 flex-1">
								<div class="flex flex-wrap items-center gap-2">
									<span class="text-sm font-medium">
										{#if liveDeploy.status === 'running'}
											Deploying…
										{:else if liveDeploy.status === 'success'}
											Deploy succeeded
										{:else}
											Deploy failed
										{/if}
									</span>
									{#if liveDeploy.status === 'running'}
										<Badge class="border-transparent bg-amber-500/15 text-amber-500">building</Badge>
									{:else if liveDeploy.status === 'success'}
										<Badge class="border-transparent bg-emerald-500/15 text-emerald-500">success</Badge>
									{:else}
										<Badge variant="destructive">failed</Badge>
									{/if}
									<span class="text-muted-foreground text-xs" title={fmtDate(liveDeploy.started)}
										>{ago(liveDeploy.started)}</span
									>
								</div>
								<div class="text-muted-foreground mt-0.5 truncate font-mono text-xs">{liveDeploy.source}</div>
							</div>
							<Button
								variant="ghost"
								size="sm"
								class="shrink-0"
								onclick={() => (deployLogOpen = !deployLogOpen)}
							>
								<ChevronDownIcon
									class="size-4 transition-transform duration-200 {deployLogOpen ? 'rotate-180' : ''}"
								/>
								{deployLogOpen ? 'Hide log' : 'Show log'}
							</Button>
						</div>
						{#if deployLogOpen}
							<div
								bind:this={deployLogEl}
								class="border-border/60 bg-card/60 max-h-72 overflow-y-auto border-t p-3 font-mono text-xs leading-5"
							>
								{#each liveDeploy.lines as line, i (i)}
									<div
										class="whitespace-pre-wrap {line.startsWith('[error]') || line.includes('aborted')
											? 'text-red-400'
											: line.includes('[gantry] done')
												? 'text-emerald-400'
												: line.startsWith('[check]') || line.startsWith('[gantry]')
													? 'text-amber-400/90'
													: ''}"
									>
										{line}
									</div>
								{:else}
									<p class="text-muted-foreground animate-pulse">Starting deploy…</p>
								{/each}
							</div>
						{/if}
					</div>
				{/if}

				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Deploy history</Card.Title>
						<Card.Description>
							Every deploy of this app, newest first. Open the log to see exactly what happened
							during the build. The last 20 deploys are kept.
						</Card.Description>
					</Card.Header>
					<Card.Content class="grid gap-3">
						{#each deploys as e (e.id)}
							<div
								class="flex items-center gap-3 rounded-lg border p-3 transition-colors
									{e.status === 'running' ? 'border-amber-500/30 bg-amber-500/5' : ''}"
							>
								<span
									class="size-2 shrink-0 rounded-full {e.status === 'success'
										? 'bg-emerald-500'
										: e.status === 'running'
											? 'animate-pulse bg-amber-500'
											: 'bg-red-500'}"
								></span>
								<div class="min-w-0 flex-1">
									<div class="flex items-center gap-2">
										<span class="text-sm font-medium capitalize">{e.status}</span>
										{#if deployDuration(e)}
											<span class="text-muted-foreground text-xs">took {deployDuration(e)}</span>
										{/if}
									</div>
									<div class="text-muted-foreground truncate font-mono text-xs">
										{e.source || 'unknown source'}
									</div>
								</div>
								{#if e.started}
									<span class="text-muted-foreground shrink-0 text-xs" title={fmtDate(e.started)}>{ago(e.started)}</span>
								{/if}
								<Button variant="outline" size="sm" onclick={() => viewDeployLog(e)}>
									<ScrollTextIcon class="size-4" /> Log
								</Button>
							</div>
						{:else}
							<p class="text-muted-foreground text-sm">
								{deploysLoading
									? 'Loading…'
									: liveDeploy
										? 'History will update when this deploy finishes.'
										: 'No deploys yet. Hit the Deploy button and it will show up here.'}
							</p>
						{/each}
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
									{#if domain.proxied}
										<span
											class="text-muted-foreground flex items-center gap-1 text-xs"
											title="This domain runs through Cloudflare's proxy. Traffic reaches this server via Cloudflare, so the panel can't compare IPs directly."
										>
											<CloudIcon class="size-3.5" /> via Cloudflare
										</span>
									{/if}
									{#if d.ssl && domain.dnsOk}
										<LockIcon class="size-3.5 text-emerald-500" />
									{:else if !domain.dnsOk}
										<span class="flex items-center gap-1.5 text-xs text-amber-500" title="Point this domain's DNS at the server IP, checked every 30s">
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
						<Card.Title class="flex items-center gap-2 text-base">
							Maintenance mode
							{#if d.maintenance}
								<Badge class="border-transparent bg-amber-500/15 text-amber-500">on</Badge>
							{/if}
						</Card.Title>
						<Card.Description>
							Puts a temporary "be right back" page in front of this app while you work on it.
							Visitors see that page instead of your site until you turn it off.
						</Card.Description>
					</Card.Header>
					<Card.Content class="grid gap-4">
						<div class="grid gap-3 sm:grid-cols-3">
							{#each MAINT_TEMPLATES as t (t.id)}
								<button
									type="button"
									class="rounded-lg border p-2.5 text-left transition-colors {maintTpl === t.id
										? 'border-primary ring-primary/25 ring-2'
										: 'hover:border-muted-foreground/40'}"
									onclick={() => (maintTpl = t.id)}
								>
									{#if t.id === 'clean'}
										<div class="mb-2 flex h-16 flex-col items-center justify-center gap-1.5 rounded-md" style="background:#f8fafc">
											<div class="size-1.5 rounded-full bg-amber-500"></div>
											<div class="h-1.5 w-16 rounded bg-slate-800/80"></div>
											<div class="h-1 w-24 rounded bg-slate-400/60"></div>
										</div>
									{:else if t.id === 'dark'}
										<div class="mb-2 flex h-16 flex-col items-center justify-center gap-1.5 rounded-md" style="background:#0d1117">
											<div class="size-3 rounded-full border-2 border-slate-700 border-t-sky-400"></div>
											<div class="h-1.5 w-16 rounded bg-slate-200/80"></div>
											<div class="h-1 w-24 rounded bg-slate-500/60"></div>
										</div>
									{:else}
										<div class="relative mb-2 flex h-16 flex-col items-center justify-center gap-1 overflow-hidden rounded-md" style="background:#fffbeb">
											<div class="absolute inset-x-0 top-0 h-1.5" style="background:repeating-linear-gradient(45deg,#f59e0b 0 5px,#1c1917 5px 10px)"></div>
											<div class="text-sm leading-none">🚧</div>
											<div class="h-1.5 w-16 rounded bg-stone-800/80"></div>
										</div>
									{/if}
									<div class="text-sm font-medium">{t.label}</div>
									<p class="text-muted-foreground text-xs">{t.desc}</p>
								</button>
							{/each}
						</div>
						<div class="flex flex-wrap items-center gap-3">
							<Button
								variant="outline"
								size="sm"
								href="/api/maintenance/preview?template={maintTpl}&app={d.name}"
								target="_blank"
							>
								<ExternalLinkIcon class="size-4" /> Preview page
							</Button>
							{#if d.maintenance && maintTpl !== (d.maintenanceTpl || 'clean')}
								<Button size="sm" onclick={() => setMaintenance(true)} disabled={maintBusy}>
									Switch to this page
								</Button>
							{/if}
							<div class="ml-auto flex items-center gap-2">
								<WrenchIcon class="text-muted-foreground size-4" />
								<span class="text-sm {d.maintenance ? 'text-amber-500' : 'text-muted-foreground'}">
									{maintBusy ? 'Working…' : d.maintenance ? 'Maintenance on' : 'Maintenance off'}
								</span>
								<Switch
									checked={d.maintenance}
									onCheckedChange={(v) => setMaintenance(v)}
									disabled={maintBusy}
									aria-label="Maintenance mode on or off"
								/>
							</div>
						</div>
					</Card.Content>
				</Card.Root>

				<Card.Root>
					<Card.Header>
						<Card.Title class="flex items-center gap-2 text-base">
							<HardDriveIcon class="size-4" /> Persistent storage
						</Card.Title>
						<Card.Description>
							Apps lose their files on every deploy and restart. Attach a folder here and anything the
							app saves in it (uploads, a SQLite file, generated images) survives. Gantry stores it
							safely on the server for you.
						</Card.Description>
					</Card.Header>
					<Card.Content>
						<div class="divide-border overflow-hidden rounded-lg border divide-y">
							{#each d.mounts as m (m.containerPath)}
								<div class="bg-muted/30 flex items-center gap-2 px-3 py-2">
									<HardDriveIcon class="text-muted-foreground size-3.5 shrink-0" />
									<span class="truncate font-mono text-xs">{m.containerPath}</span>
									<span class="text-muted-foreground ml-auto hidden truncate font-mono text-xs sm:block" title="Where the files live on the server">
										{m.hostDir}
									</span>
									<button
										class="text-muted-foreground hover:text-destructive p-1.5"
										onclick={() => modMount(m.containerPath, true)}
										disabled={mountBusy}
										aria-label="Detach {m.containerPath}"
									>
										<Trash2Icon class="size-4" />
									</button>
								</div>
							{:else}
								<p class="text-muted-foreground px-3 py-2 text-sm">No folders attached.</p>
							{/each}
							<div class="flex items-center gap-2 px-3 py-2">
								<Input
									bind:value={newMount}
									placeholder="/data/uploads"
									class="h-8 flex-1 border-0 bg-transparent! font-mono text-xs shadow-none focus-visible:ring-0"
									onkeydown={(e) => e.key === 'Enter' && newMount.trim() && modMount(newMount.trim(), false)}
								/>
								<Button
									variant="ghost"
									size="sm"
									disabled={!newMount.trim() || mountBusy}
									onclick={() => modMount(newMount.trim(), false)}
								>
									<PlusIcon class="size-4" /> Attach folder
								</Button>
							</div>
						</div>
						<p class="text-muted-foreground mt-2 text-xs">
							Enter the folder path as your app sees it inside its container. Attaching or detaching
							restarts the app so the change takes effect.
						</p>
					</Card.Content>
				</Card.Root>

				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Project</Card.Title>
						<Card.Description>
							Joining a project gives this app the project's shared environment variables.
						</Card.Description>
					</Card.Header>
					<Card.Content class="max-w-sm">
						<select
							class="border-input h-9 w-full rounded-md border bg-transparent px-2 text-sm"
							bind:value={category}
							onchange={saveCategory}
							disabled={savingCategory}
						>
							<option value="">Unassigned</option>
							{#each d.projects ?? [] as p (p)}<option value={p}>{p}</option>{/each}
						</select>
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

				<Card.Root class="border-destructive/40">
					<Card.Header>
						<Card.Title class="text-destructive text-base">Danger zone</Card.Title>
						<Card.Description>
							Destroys the app, its containers, config and domains. This cannot be undone,
							type <code class="text-foreground">{d.name}</code> to confirm.
						</Card.Description>
					</Card.Header>
					<Card.Content class="flex max-w-md gap-2">
						<Input
							bind:value={destroyConfirm}
							placeholder={d.name}
							class="font-mono text-xs"
							autocapitalize="off"
							autocorrect="off"
							spellcheck={false}
						/>
						<Button
							variant="destructive"
							class="shrink-0"
							disabled={destroyConfirm.trim().toLowerCase() !== d.name.toLowerCase() || destroying}
							onclick={destroyApp}
						>
							<Trash2Icon class="size-4" />
							{destroying ? 'Destroying…' : 'Destroy app'}
						</Button>
					</Card.Content>
				</Card.Root>
			</Tabs.Content>

			<Tabs.Content value="env" class="mt-4">
				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Environment variables</Card.Title>
						<Card.Description>
							Saved with <code>--no-restart</code>; toggle below to restart after saving.
							{#if d.category}
								Variables marked "project" come from the {d.category} project's shared env;
								change one here to override it for this app only.
							{/if}
						</Card.Description>
					</Card.Header>
					<Card.Content class="grid gap-2">
						<EnvEditor env={editEnv} inheritedEnv={d.projectEnv ?? {}} onsave={saveEnv} />
						{#if sysEnv.length}
							<details class="mt-3">
								<summary class="text-muted-foreground cursor-pointer text-xs">
									System variables ({sysEnv.length}), managed by the deploy system
								</summary>
								<div class="bg-muted/30 mt-2 grid gap-1 rounded-md border p-3">
									{#each sysEnv as [k, v] (k)}
										<div class="text-muted-foreground flex gap-3 font-mono text-xs">
											<span class="w-52 shrink-0">{k}</span>
											<span class="truncate">{v}</span>
										</div>
									{/each}
									<p class="text-muted-foreground mt-1 text-xs font-sans">
										Set automatically by dokku (build type, ports, deployed commit). Read-only
										here, changing them by hand can break the app.
									</p>
								</div>
							</details>
						{/if}
					</Card.Content>
				</Card.Root>
			</Tabs.Content>

			<Tabs.Content value="cron" class="mt-4">
				<Card.Root>
					<Card.Header>
						<Card.Title class="text-base">Scheduled jobs</Card.Title>
						<Card.Description>
							Run commands automatically on a schedule, like nightly cleanups or report scripts.
							Each run starts a fresh copy of {d.name}, runs the command, and exits. Times are in
							your timezone ({userTzFull()}); cron runs on the server ({serverTzLabel() || 'server'}).
							Changes apply as soon as you save, no redeploy needed.
						</Card.Description>
					</Card.Header>
					<Card.Content class="grid gap-2">
						{#each jobs as job, i (job.id || i)}
							{@const badge = lastBadge(job.last)}
							<div class="flex flex-wrap items-start gap-3 rounded-md border p-3 {job.disabled ? 'opacity-70' : ''}">
								<div class={job.disabled ? 'pointer-events-none opacity-60' : ''}>
									<CronInput bind:value={job.schedule} />
								</div>
								<div class="grid min-w-56 flex-1 gap-1">
									<Input class="font-mono text-xs" placeholder="node scripts/cleanup.js" bind:value={job.command} />
									<p class="text-muted-foreground text-xs">Command to run inside the app</p>
								</div>
								{#if badge}
									<Badge variant={badge.ok ? 'secondary' : 'destructive'} class="mt-2 whitespace-nowrap">
										{badge.label}
									</Badge>
								{/if}
								<div class="mt-2 flex items-center gap-2">
									<span class="text-xs {job.disabled ? 'text-muted-foreground' : 'text-emerald-500'}">
										{job.disabled ? 'Off' : 'On'}
									</span>
									<Switch
										checked={!job.disabled}
										onCheckedChange={(v) => {
											job.disabled = !v;
											if (job.id) saveCron();
										}}
										aria-label="Job on or off"
									/>
									<Button
										variant="ghost"
										size="icon"
										onclick={() => (jobs = jobs.filter((_, j) => j !== i))}
										aria-label="Remove job"
									>
										<Trash2Icon class="size-4" />
									</Button>
								</div>
							</div>
						{:else}
							<p class="text-muted-foreground text-sm">No jobs yet. Add one and pick how often it runs.</p>
						{/each}
						<div class="mt-2 flex items-center gap-2">
							<Button
								variant="outline"
								size="sm"
								onclick={() => jobs.push({ id: '', schedule: '0 3 * * *', command: '' })}
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

			<Tabs.Content value="logs" class="mt-4 grid gap-3">
				<div class="flex flex-wrap items-center gap-2">
					<span class="flex items-center gap-1.5 text-xs font-medium text-emerald-500">
						<span class="size-1.5 animate-pulse rounded-full bg-emerald-500"></span>
						live
					</span>
					<select
						class="border-input text-muted-foreground h-7 rounded-md border bg-transparent px-2 text-xs"
						bind:value={histHours}
						onchange={changeRange}
					>
						<option value={1}>Last hour</option>
						<option value={6}>Last 6 hours</option>
						<option value={24}>Last 24 hours</option>
						<option value={72}>Last 3 days</option>
						<option value={168}>Last 7 days</option>
					</select>
					<div class="flex overflow-hidden rounded-md border">
						{#each [['', 'All'], ['w', 'Warnings'], ['e', 'Errors']] as [v, label] (v)}
							<button
								class="px-2.5 py-1.5 text-xs font-medium not-first:border-l {histSev === v
									? 'bg-accent text-accent-foreground'
									: 'text-muted-foreground hover:text-foreground'}"
								onclick={() => (histSev = v)}
							>
								{label}
							</button>
						{/each}
					</div>
					<Input bind:value={histFilter} placeholder="Filter…" class="h-7 w-40 text-xs" />
					<span class="text-muted-foreground ml-auto text-xs">
						kept for {histRetention} days
					</span>
				</div>

				<div class="rounded-lg border p-3">
						<svg viewBox="0 0 480 64" preserveAspectRatio="none" class="h-16 w-full" role="img" aria-label="Log volume over time, colored by severity">
							{#each histBuckets as b, i (i)}
								{@const h = (b.total / histMax) * 56}
								{@const he = b.total ? (b.e / b.total) * h : 0}
								{@const hw = b.total ? (b.w / b.total) * h : 0}
								<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
								<g
									class="cursor-pointer"
									opacity={histBucket === -1 || histBucket === i ? 1 : 0.35}
									onclick={() => (histBucket = histBucket === i ? -1 : i)}
								>
									<rect x={i * 10 + 1.5} y="0" width="7" height="64" fill="transparent" />
									{#if b.total}
										<rect x={i * 10 + 1.5} y={60 - h} width="7" height={h - he - hw} rx="1" fill="#8b8bef" />
										{#if hw}<rect x={i * 10 + 1.5} y={60 - he - hw} width="7" height={hw} fill="#f59e0b" />{/if}
										{#if he}<rect x={i * 10 + 1.5} y={60 - he} width="7" height={he} fill="#ef4444" />{/if}
									{:else}
										<rect x={i * 10 + 1.5} y="58" width="7" height="2" rx="1" fill="#8b8bef" opacity="0.35" />
									{/if}
								</g>
							{/each}
						</svg>
						<div class="text-muted-foreground flex justify-between font-mono text-[10px]">
							<span>{histTime(histRange.start, true)}</span>
							<span>{histTime(histRange.start + (histRange.end - histRange.start) / 2, true)}</span>
							<span>{histTime(histRange.end, true)}</span>
						</div>
						{#if histBucket >= 0}
							<button class="text-muted-foreground hover:text-foreground mt-1 text-xs underline" onclick={() => (histBucket = -1)}>
								Showing {histTime(histRange.start + histBucket * histRange.span, true)} to
								{histTime(histRange.start + (histBucket + 1) * histRange.span, true)}, click to clear
							</button>
						{/if}
					</div>
				<div class="relative">
					<div
						bind:this={logEl}
						onscroll={() => (logAtBottom = !logEl || logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 60)}
						class="bg-card h-[26rem] overflow-y-auto rounded-lg border p-4 font-mono text-xs leading-5"
					>
						{#each visibleHist as l (l.t + l.line)}
							<div class="flex gap-3 whitespace-pre-wrap {l.sev === 'e' ? 'text-red-400' : l.sev === 'w' ? 'text-amber-400' : ''}">
								<span class="text-muted-foreground shrink-0">{histTime(l.t)}</span>
								<span>{l.line}</span>
							</div>
						{:else}
							<p class="text-muted-foreground">
								{histLoading
									? 'Loading…'
									: histLines.length
										? 'Nothing matches the current filters.'
										: 'No stored logs yet. Gantry records app output from now on, so history fills up as the app runs.'}
							</p>
						{/each}
					</div>
					{#if !logAtBottom || histBucket >= 0}
						<Button
							size="sm"
							class="absolute bottom-3 left-1/2 -translate-x-1/2 shadow-lg"
							onclick={() => {
								histBucket = -1;
								logAtBottom = true;
								if (logEl) logEl.scrollTop = logEl.scrollHeight;
							}}
						>
							<ChevronDownIcon class="size-4" /> Jump to live
						</Button>
					{/if}
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

<Dialog.Root bind:open={histLogOpen}>
	<Dialog.Content class="max-w-2xl">
		<Dialog.Header>
			<Dialog.Title>Deploy log</Dialog.Title>
			<Dialog.Description>
				{#if histLog}
					{fmtDate(histLog.started)}{histLog.source ? ', ' + histLog.source : ''}
				{/if}
			</Dialog.Description>
		</Dialog.Header>
		<pre
			class="bg-card mt-2 max-h-96 overflow-auto rounded-md border p-3 font-mono text-xs leading-5 whitespace-pre-wrap">{histLogText ||
				'Loading…'}</pre>
	</Dialog.Content>
</Dialog.Root>
