// Shared list states: loading, empty, and error with a retry.

export function Loading({ label = 'Loading' }: { label?: string }) {
  return (
    <p className="state state--loading" role="status">
      {label}…
    </p>
  );
}

export function Empty({ children }: { children: React.ReactNode }) {
  return <p className="state state--empty">{children}</p>;
}

export function Failure({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="state state--error" role="alert">
      <p>{message}</p>
      {onRetry && (
        <button type="button" className="btn btn--ghost" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

export function Notice({ tone, children }: { tone: 'ok' | 'error'; children: React.ReactNode }) {
  return (
    <p className={`notice notice--${tone}`} role={tone === 'error' ? 'alert' : 'status'}>
      {children}
    </p>
  );
}
