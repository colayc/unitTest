export interface Diagnostic {
    code:       string;
    column?:    number;
    line?:      number;
    message:    string;
    severity:   Severity;
    sourceUri?: string;
}

export enum Severity {
    Error = "error",
    Info = "info",
    Warning = "warning",
}
