import type { ReactNode } from 'react';

/** The centred card used by the signed-out screens (verify, forgot and reset password). A screen with nothing to do has no children. */
export function AuthShell({ title, description, children }: { title: string; description?: string; children?: ReactNode }) {
  return (
    <main id="main" tabIndex={-1} className="flex min-h-screen items-center justify-center px-4 focus:outline-none">
      <div className="w-full max-w-sm">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {/* Descriptions may carry an address or an agent's name, which must wrap instead of widening the page at 390 px. */}
        {description ? <p className="mt-1 text-sm text-muted-foreground [overflow-wrap:anywhere]">{description}</p> : null}
        {children ? <div className="mt-8">{children}</div> : null}
      </div>
    </main>
  );
}
