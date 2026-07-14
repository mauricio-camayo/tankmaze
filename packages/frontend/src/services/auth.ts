import { Amplify } from 'aws-amplify';
import {
  signIn as amplifySignIn,
  signOut as amplifySignOut,
  signUp as amplifySignUp,
  confirmSignUp as amplifyConfirmSignUp,
  resendSignUpCode as amplifyResendSignUpCode,
  getCurrentUser,
  fetchAuthSession,
  signInWithRedirect,
  updatePassword as amplifyUpdatePassword,
  confirmResetPassword as amplifyConfirmResetPassword,
} from 'aws-amplify/auth';
import { getMyProfile } from './api';

const LOCAL_DEV = import.meta.env.VITE_LOCAL_DEV === 'true';
const LOCAL_USER = { userId: 'local-user', username: 'local' };

export function configureAmplify() {
  if (LOCAL_DEV) return;
  const oauthDomain = import.meta.env.VITE_COGNITO_DOMAIN as string | undefined;
  Amplify.configure({
    Auth: {
      Cognito: {
        userPoolId: import.meta.env.VITE_USER_POOL_ID as string,
        userPoolClientId: import.meta.env.VITE_USER_POOL_CLIENT_ID as string,
        ...(oauthDomain && {
          loginWith: {
            oauth: {
              domain: oauthDomain,
              scopes: ['email', 'profile', 'openid'],
              redirectSignIn: [window.location.origin],
              redirectSignOut: [window.location.origin],
              responseType: 'code' as const,
            },
          },
        }),
      },
    },
  });
}

export async function signInWithGoogle() {
  if (LOCAL_DEV) return;
  return signInWithRedirect({ provider: 'Google' });
}

export async function signInWithFacebook() {
  if (LOCAL_DEV) return;
  return signInWithRedirect({ provider: 'Facebook' });
}

// GitHub (item 233) and Discord (item 240) aren't one of Amplify's built-in
// `provider` string literals (Google/Facebook/Amazon/Apple) — both are
// wired as custom OIDC providers (see auth-stack.ts's oidc-shim), addressed
// by the exact name Cognito registered them under.
export async function signInWithGithub() {
  if (LOCAL_DEV) return;
  return signInWithRedirect({ provider: { custom: 'GitHub' } });
}

export async function signInWithDiscord() {
  if (LOCAL_DEV) return;
  return signInWithRedirect({ provider: { custom: 'Discord' } });
}

export async function signIn(username: string, password: string) {
  if (LOCAL_DEV) return { isSignedIn: true, nextStep: { signInStep: 'DONE' } };
  return amplifySignIn({ username, password });
}

export async function signUpWithEmail(email: string, password: string) {
  if (LOCAL_DEV) return { isSignUpComplete: false, nextStep: { signUpStep: 'CONFIRM_SIGN_UP' } };
  return amplifySignUp({ username: email, password, options: { userAttributes: { email } } });
}

export async function confirmEmailSignUp(email: string, code: string) {
  if (LOCAL_DEV) return { isSignUpComplete: true };
  return amplifyConfirmSignUp({ username: email, confirmationCode: code });
}

export async function resendConfirmationCode(email: string) {
  if (LOCAL_DEV) return;
  return amplifyResendSignUpCode({ username: email });
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

export async function getUserProfile(): Promise<{ sub?: string; name?: string; picture?: string; email?: string; isAdmin?: boolean; isFederated?: boolean }> {
  if (LOCAL_DEV) return { name: 'Local User', email: 'dev@localhost', isAdmin: true, isFederated: false };
  try {
    const session = await fetchAuthSession();
    const payload = session.tokens?.idToken?.payload;
    if (!payload) return {};
    const groups = payload['cognito:groups'] as string[] | undefined;
    return {
      sub: payload['sub'] as string | undefined,
      name: (payload['given_name'] ?? payload['name']) as string | undefined,
      picture: payload['picture'] as string | undefined,
      email: payload['email'] as string | undefined,
      isAdmin: groups?.includes('platform-admin') ?? false,
      // Federated (Google/Facebook) sign-ins carry an "identities" claim on
      // the ID token; native email+password Cognito users never have one —
      // used to hide the password-change section for accounts with no
      // Cognito password to change (item 216).
      isFederated: payload['identities'] != null,
    };
  } catch {
    return {};
  }
}

export interface EnrichedAuthUser {
  userId: string;
  username: string;
  name?: string;
  picture?: string;
  email?: string;
  isAdmin?: boolean;
  isFederated?: boolean;
}

// Shared by App.tsx's checkUser() (session restore / federated-redirect
// return) and Landing.tsx's email+password sign-in (item 228) — both need
// the same JWT-derived profile plus the durable backend display name
// (item 225's fix for federated re-login clobbering given_name), not just
// the bare username Amplify hands back on a successful signIn().
export async function enrichAuthUser(u: { userId: string; username: string }): Promise<EnrichedAuthUser> {
  const profile = await getUserProfile();
  let name = profile.name;
  let picture = profile.picture;
  try {
    const durable = await getMyProfile();
    name = durable.name || name;
    // Empty means "never uploaded one" (item 229) — keep the JWT-derived
    // picture in that case rather than blanking out a federated avatar.
    picture = durable.picture || picture;
  } catch {
    // keep the JWT-derived name/picture
  }
  return { userId: profile.sub ?? u.userId, username: u.username, name, picture, email: profile.email, isAdmin: profile.isAdmin, isFederated: profile.isFederated };
}

export async function changePassword(oldPassword: string, newPassword: string) {
  if (LOCAL_DEV) return;
  return amplifyUpdatePassword({ oldPassword, newPassword });
}

// Applies the code from a forgot-password email — distinct from item 217's
// own /auth/forgot-password trigger (services/api.ts's requestPasswordReset),
// which is the enumeration-safe step. By the time a user has a real code in
// hand, enumeration safety no longer applies, so this calls Cognito directly.
export async function confirmPasswordReset(email: string, code: string, newPassword: string) {
  if (LOCAL_DEV) return;
  return amplifyConfirmResetPassword({ username: email, confirmationCode: code, newPassword });
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
