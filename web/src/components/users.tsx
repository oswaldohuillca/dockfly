import { useContext, useState } from 'react';
import { Copy, KeyRound, Plus, Trash2, UserRound } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { api, type PasswordLink, type Role, type UserEntry } from '@/lib/api';
import { formatDate } from '@/lib/format';
import { roleHint, roleLabel, UserContext } from '@/lib/roles';
import { useSubmit } from '@/lib/use-form';
import { useAction, useResource } from '@/lib/use-resource';

/** Usuarios del panel y sus roles (solo administradores). */
export function Users() {
  const { t } = useLingui();
  const me = useContext(UserContext);
  const { data: users = [], reload } = useResource(api.listUsers);
  const [editing, setEditing] = useState<UserEntry | 'new' | null>(null);
  const [link, setLink] = useState<(PasswordLink & { user: UserEntry; invite: boolean }) | null>(null);
  const run = useAction();

  const newLink = (u: UserEntry) =>
    run(async () => setLink({ ...(await api.userLink(u.id)), user: u, invite: u.pending }));

  function remove(u: UserEntry) {
    const name = u.name;
    return run(() => api.deleteUser(u.id), { success: t`${name} no longer has access`, then: reload });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>Users</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Invite your team with a link. Everyone signs in with their own email and password (and their own two-factor
            authentication).
          </Trans>
        </CardDescription>
        <CardAction>
          <Button variant="outline" onClick={() => setEditing('new')}>
            <Plus /> <Trans>Invite</Trans>
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="px-0">
        <ul className="divide-y">
          {users.map((u) => {
            const name = u.name;
            const since = formatDate(u.created_at);
            return (
              <li key={u.id} className="flex flex-wrap items-center gap-3 px-6 py-3">
                <UserRound className="size-4 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2 font-medium">
                    {u.name}
                    {u.id === me?.id && (
                      <span className="text-xs font-normal text-muted-foreground">
                        <Trans>(you)</Trans>
                      </span>
                    )}
                    {u.pending && (
                      <Badge variant="outline">
                        <Trans>Pending invitation</Trans>
                      </Badge>
                    )}
                    {u.totp_enabled && <Badge variant="secondary">2FA</Badge>}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {u.email} · <Trans>since {since}</Trans>
                  </div>
                </div>
                <Badge variant={u.role === 'admin' ? 'default' : 'secondary'}>{roleLabel(u.role)}</Badge>
                <Button variant="ghost" size="sm" onClick={() => setEditing(u)}>
                  <Trans>Edit</Trans>
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => newLink(u)}
                  aria-label={u.pending ? t`New invitation link for ${name}` : t`Reset password for ${name}`}
                  title={u.pending ? t`New invitation link` : t`Password reset link`}
                >
                  <KeyRound />
                </Button>
                {u.id !== me?.id && (
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-destructive hover:text-destructive"
                    onClick={() => remove(u)}
                    aria-label={t`Remove access for ${name}`}
                  >
                    <Trash2 />
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      </CardContent>
      {editing && (
        <UserDialog
          user={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={(res) => {
            setEditing(null);
            if (res) setLink({ ...res, invite: true });
            reload();
          }}
        />
      )}
      {link && <LinkDialog link={link} onClose={() => setLink(null)} />}
    </Card>
  );
}

function UserDialog({
  user,
  onClose,
  onSaved,
}: {
  user?: UserEntry;
  onClose: () => void;
  onSaved: (invite?: PasswordLink & { user: UserEntry }) => void;
}) {
  const { t } = useLingui();
  const [name, setName] = useState(user?.name ?? '');
  const [email, setEmail] = useState(user?.email ?? '');
  const [role, setRole] = useState<Role>(user?.role ?? 'deployer');
  const userName = user?.name;

  const { submit, busy, error } = useSubmit(async () => {
    if (user) {
      await api.updateUser(user.id, { name, role });
      toast.success(t`Changes saved`);
      onSaved();
    } else {
      onSaved(await api.inviteUser({ name, email, role }));
    }
  });

  return (
    <FormDialog
      className="sm:max-w-md"
      title={user ? t`Edit ${userName}` : t`Invite someone`}
      description={user ? t`Role changes apply immediately.` : t`You'll get a link to send them however you like.`}
      onClose={onClose}
      onSubmit={submit}
      busy={busy}
      error={error}
      submitLabel={user ? <Trans>Save</Trans> : <Trans>Create invitation</Trans>}
    >
      <div className="space-y-2">
        <Label htmlFor="user-name">
          <Trans>Name</Trans>
        </Label>
        <Input id="user-name" value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
      </div>
      {!user && (
        <div className="space-y-2">
          <Label htmlFor="user-email">Email</Label>
          <Input id="user-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </div>
      )}
      <div className="space-y-2">
        <Label>
          <Trans>Role</Trans>
        </Label>
        <Select value={role} onValueChange={(v) => setRole(v as Role)}>
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(['admin', 'deployer', 'viewer'] as const).map((r) => (
              <SelectItem key={r} value={r}>
                {roleLabel(r)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className="text-xs text-muted-foreground">{roleHint(role)}</p>
      </div>
    </FormDialog>
  );
}

function LinkDialog({
  link,
  onClose,
}: {
  link: PasswordLink & { user: UserEntry; invite: boolean };
  onClose: () => void;
}) {
  const { t } = useLingui();
  const userName = link.user.name;
  const expires = formatDate(link.expires_at);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{link.invite ? t`Invitation for ${userName}` : t`Reset password for ${userName}`}</DialogTitle>
          <DialogDescription>
            <Trans>
              Send them this link to choose their password. It can only be used once and expires on {expires}.
            </Trans>
            {!link.invite && ' ' + t`Their current password keeps working until they use it.`}
          </DialogDescription>
        </DialogHeader>
        <div className="flex gap-2">
          <Input readOnly value={link.link} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
          <Button
            variant="outline"
            onClick={() => navigator.clipboard.writeText(link.link).then(() => toast.success(t`Link copied`))}
          >
            <Copy /> <Trans>Copy</Trans>
          </Button>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>
            <Trans>Done</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
