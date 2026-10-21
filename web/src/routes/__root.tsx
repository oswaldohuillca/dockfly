import { createRootRoute, Link, Outlet } from '@tanstack/react-router';
import { Trans } from '@lingui/react/macro';
import { ThemeProvider } from 'next-themes';

import { Button } from '@/components/ui/button';
import { Toaster } from '@/components/ui/sonner';
import { TooltipProvider } from '@/components/ui/tooltip';

export const Route = createRootRoute({
  component: Root,
  notFoundComponent: NotFound,
});

function Root() {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      <TooltipProvider>
        <Outlet />
        <Toaster richColors position="bottom-right" />
      </TooltipProvider>
    </ThemeProvider>
  );
}

function NotFound() {
  return (
    <div className="grid min-h-svh place-items-center text-center">
      <div className="space-y-2">
        <p className="text-4xl font-semibold">404</p>
        <p className="text-muted-foreground"><Trans>This page doesn't exist.</Trans></p>
        <Button variant="link" asChild>
          <Link to="/">
            <Trans>Back to dashboard</Trans>
          </Link>
        </Button>
      </div>
    </div>
  );
}
