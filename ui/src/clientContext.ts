import { createContext, useContext } from "solid-js";
import { DuxClient } from "dux-client";

export const DuxClientContext = createContext<DuxClient | undefined>(undefined);

export function useDuxClient(): DuxClient {
  const client = useContext(DuxClientContext);
  if (!client) throw new Error("useDuxClient must be used within DuxClientContext.Provider");
  return client;
}
