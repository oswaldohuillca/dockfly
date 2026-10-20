import { createRootRoute, Link, Outlet } from '@tanstack/react-router';
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
        <p className="text-muted-foreground">Esta página no existe.</p>
        <Button variant="link" asChild>
          <Link to="/">Volver al panel</Link>
        </Button>
      </div>
    </div>
  );
}
