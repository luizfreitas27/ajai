// Frame is a panel with a hairline box, a mono caption top-left and a mono
// meta line top-right, e.g. "LOG" and "FOLLOWING · 500 LINES".
export function Frame({
  id,
  caption,
  meta,
  children,
  className = '',
}: {
  id?: string;
  caption: string;
  meta?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section id={id} className={`frame ${className}`} aria-labelledby={id ? `${id}-caption` : undefined}>
      <header className="frame__head">
        <h2 id={id ? `${id}-caption` : undefined} className="frame__caption">
          {caption}
        </h2>
        {meta !== undefined && <p className="frame__meta">{meta}</p>}
      </header>
      <div className="frame__body">{children}</div>
    </section>
  );
}
