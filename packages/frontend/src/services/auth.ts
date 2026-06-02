import { Amplify } from 'aws-amplify';
import {
  signIn as amplifySignIn,
  signOut as amplifySignOut,
  getCurrentUser,
  fetchAuthSession,
} from 'aws-amplify/auth';

const LOCAL_DEV = import.meta.env.VITE_LOCAL_DEV === 'true';
const LOCAL_USER = { userId: 'local-user', username: 'local' };

export function configureAmplify() {
  if (LOCAL_DEV) return;
  Amplify.configure({
    Auth: {
      Cognito: {
        userPoolId: import.meta.env.VITE_USER_POOL_ID as string,
        userPoolClientId: import.meta.env.VITE_USER_POOL_CLIENT_ID as string,
      },
    },
  });
}

export async function signIn(username: string, password: string) {
  if (LOCAL_DEV) return { isSignedIn: true, nextStep: { signInStep: 'DONE' } };
  return amplifySignIn({ username, password });
}

export async function signOut() {
  if (LOCAL_DEV) return;
  return amplifySignOut();
}

export async function getAuthUser() {
  if (LOCAL_DEV) return LOCAL_USER;
  try {
    return await getCurrentUser();
  } catch {
    return null;
  }
}

export async function getIdToken(): Promise<string | null> {
  if (LOCAL_DEV) return 'local-token';
  try {
    const session = await fetchAuthSession();
    return session.tokens?.idToken?.toString() ?? null;
  } catch {
    return null;
  }
}
