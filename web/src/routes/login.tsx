import { createFileRoute, redirect, useRouter } from '@tanstack/react-router';

import { AuthForm } from '@/components/auth-form';
import { api } from '@/lib/api';
import { getUser, safeRedirect, setUser } from '@/lib/session';

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
  }),
  // With a session already open, there is no point in showing the login.
  beforeLoad: async ({ search }) => {
    if (await getUser()) throw redirect({ href: safeRedirect(search.redirect) });
  },
  // On the first install, the same form creates the administrator.
  loader: () => api.setupStatus(),
  component: Login,
});

function Login() {
  const { needs_setup, setup_code_required } = Route.useLoaderData();
  const { redirect: target } = Route.useSearch();
  const router = useRouter();

  return (
    <AuthForm
      setup={needs_setup}
      setupCodeRequired={setup_code_required}
      onAuthenticated={(user) => {
        setUser(user);
        router.history.push(safeRedirect(target));
      }}
    />
  );
}
