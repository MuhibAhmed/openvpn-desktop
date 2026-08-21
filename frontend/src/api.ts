// Typed facade over the generated Wails bindings.
//
// Components import from here rather than reaching into ./bindings directly, so
// the generated JavaScript is adapted to our TypeScript types in exactly one
// place.

import { Events } from "@wailsio/runtime";
import {
  AppService,
  ConnectionService,
  ProfileService,
} from "../bindings/github.com/MuhibAhmed/openvpn-desktop/services";

import type {
  Answer,
  Health,
  ImportResult,
  LogLine,
  Profile,
  Settings,
  Status,
} from "./types";

export const EVENT_STATUS = "connection:status";
export const EVENT_LOG = "connection:log";
export const EVENT_IMPORTED = "profiles:imported";

// Go returns nil slices as null; the UI would rather always have an array.
const list = <T>(v: T[] | null | undefined): T[] => v ?? [];

export const api = {
  profiles: {
    list: async (): Promise<Profile[]> =>
      list((await ProfileService.List()) as Profile[] | null),

    browseAndImport: async (): Promise<ImportResult> =>
      (await ProfileService.BrowseAndImport()) as ImportResult,

    rename: async (id: string, name: string): Promise<Profile> =>
      (await ProfileService.Rename(id, name)) as Profile,

    remove: (id: string): Promise<void> => ProfileService.Delete(id),

    forgetCredentials: (id: string): Promise<void> =>
      ProfileService.ForgetCredentials(id),

    savedUsername: (id: string): Promise<string> =>
      ProfileService.SavedUsername(id),
  },

  connection: {
    connect: (profileId: string): Promise<void> =>
      ConnectionService.Connect(profileId),

    disconnect: (): Promise<void> => ConnectionService.Disconnect(),

    status: async (): Promise<Status> =>
      (await ConnectionService.Status()) as Status,

    logs: async (): Promise<LogLine[]> =>
      list((await ConnectionService.Logs()) as LogLine[] | null),

    submitCredentials: (answer: Answer): Promise<void> =>
      ConnectionService.SubmitCredentials(answer as never),

    savedCredentials: async (): Promise<{
      found: boolean;
      username: string;
      password: string;
      fromLegacyGui: boolean;
    }> => (await ConnectionService.SavedCredentialsForCurrent()) as never,
  },

  app: {
    health: async (): Promise<Health> => (await AppService.Health()) as Health,

    settings: async (): Promise<Settings> =>
      (await AppService.Settings()) as Settings,

    saveSettings: async (next: Settings): Promise<Settings> =>
      (await AppService.SaveSettings(next as never)) as Settings,

    openLogFolder: (): Promise<void> => AppService.OpenLogFolder(),
    openProfileFolder: (): Promise<void> => AppService.OpenProfileFolder(),
  },
};

/** on subscribes to a backend event and returns an unsubscribe function. */
export function on<T>(event: string, handler: (data: T) => void): () => void {
  return Events.On(event, (ev: { data: T }) => handler(ev.data));
}

/**
 * errorMessage pulls a readable sentence out of whatever a rejected binding
 * call threw. The Go side already writes errors for people to read, so the goal
 * is to surface that text rather than decorate it.
 */
export function errorMessage(err: unknown): string {
  if (!err) return "Something went wrong.";
  if (typeof err === "string") return err;
  if (err instanceof Error) return err.message;
  const maybe = err as { message?: string };
  return maybe.message ?? String(err);
}
