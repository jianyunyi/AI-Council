export type Task = { id:string; state:string; requirement:string; planVersion?:number }
export type APIError = { code:string; message:string; fields?:Record<string,string> }
export type Envelope<T> = { data:T; error?:APIError; request_id:string }
