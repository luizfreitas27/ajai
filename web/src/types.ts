// Shapes of the /api responses, as internal/web/handlers.go writes them.

export type Holder = {
  pid: number;
  origin: string;
  from?: string;
  window?: number;
  started: string;
};

export type JobStep = 'opening' | 'screening' | 'applying' | 'messaging';

// The job the run is on right now. Absent between jobs.
export type CurrentJob = {
  id: string;
  title: string;
  company: string;
  location: string;
  step: JobStep;
  since: string;
};

export type RunState = {
  run_id: number;
  market: string;
  dry_run: boolean;
  term: string;
  applied: number;
  external: number;
  failed: number;
  skipped: number;
  would_apply: number;
  next_run?: string;
  job?: CurrentJob;
};

export type RunKind = 'idle' | 'starting' | 'running' | 'waiting' | 'stale';

export type Run = {
  id: number;
  started_at: string;
  ended_at: string;
  market: string;
  dry_run: boolean;
  applied: number;
  external: number;
  failed: number;
  skipped: number;
  pending_answers: number;
  reason: string;
};

export type Status = {
  running: boolean;
  kind: RunKind;
  holder: Holder | null;
  state: RunState | null;
  last_runs: Run[];
  error?: string;
};

export type Job = {
  job_id: string;
  outcome: string;
  reason: string;
  title: string;
  company: string;
  work_location: string;
  work_style: string;
  job_link: string;
  external_link: string;
  hr_name: string;
  hr_link: string;
  date_applied: string;
  recorded_at: string;
  run_id: number;
};

export type MessageState = 'queued' | 'approved' | 'sending' | 'sent' | 'failed' | 'no_recipient';

export type Message = {
  id: number;
  state: MessageState;
  job_id: string;
  title: string;
  company: string;
  recipient_name: string;
  recipient_url: string;
  text: string;
  digest: string;
  error: string;
  sent_at: string;
  created_at: string;
};

export type MessagesResponse = {
  messages: Message[];
  queue_digest: string;
  queued: number;
};

export type Invite = {
  id: number;
  state: 'queued' | 'sent' | 'already' | 'unavailable' | 'failed';
  job_id: string;
  title: string;
  company: string;
  recipient_name: string;
  recipient_url: string;
  error: string;
  sent_at: string;
  created_at: string;
};

export type Answer = {
  id: string;
  label: string;
  kind: string;
  answer: string;
  source: string;
  state: 'pending' | 'approved';
  uses: number;
  options: string[] | null;
  last_job_link: string;
  updated_at: string;
  digest: string;
};

export type LogLine = {
  key: number;
  time: string;
  level: string;
  msg: string;
  fields: [string, string][];
};
