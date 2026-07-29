import type { ProtocolTaskEvent } from "./envelopes.js";

export class EventSubscription implements AsyncIterable<ProtocolTaskEvent> {
  readonly #queue: ProtocolTaskEvent[] = [];
  readonly #waiters: Array<(value: IteratorResult<ProtocolTaskEvent>) => void> = [];
  #closed = false;
  lastSequence: number;

  get closed(): boolean { return this.#closed; }

  constructor(afterSequence: number) {
    if (!Number.isSafeInteger(afterSequence) || afterSequence < 0) {
      throw new Error("event subscription sequence must be a non-negative safe integer");
    }
    this.lastSequence = afterSequence;
  }

  push(event: ProtocolTaskEvent): boolean {
    if (this.#closed || event.sequence <= this.lastSequence) return true;
    if (!Number.isSafeInteger(event.sequence) || event.sequence !== this.lastSequence + 1) return false;
    this.lastSequence = event.sequence;
    const waiter = this.#waiters.shift();
    if (waiter) waiter({ value: event, done: false });
    else this.#queue.push(event);
    return true;
  }

  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    for (const waiter of this.#waiters.splice(0)) waiter({ value: undefined, done: true });
  }

  [Symbol.asyncIterator](): AsyncIterator<ProtocolTaskEvent> {
    return { next: () => this.next() };
  }

  next(): Promise<IteratorResult<ProtocolTaskEvent>> {
    const event = this.#queue.shift();
    if (event) return Promise.resolve({ value: event, done: false });
    if (this.#closed) return Promise.resolve({ value: undefined, done: true });
    return new Promise((resolve) => this.#waiters.push(resolve));
  }
}
