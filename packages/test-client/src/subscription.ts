import type { TaskEvent } from "@unit-test-ide/protocol-models";

export class EventSubscription implements AsyncIterable<TaskEvent> {
  readonly #queue: TaskEvent[] = [];
  readonly #waiters: Array<(value: IteratorResult<TaskEvent>) => void> = [];
  #closed = false;
  lastSequence: number;

  constructor(afterSequence: number) {
    if (!Number.isSafeInteger(afterSequence) || afterSequence < 0) {
      throw new Error("event subscription sequence must be a non-negative safe integer");
    }
    this.lastSequence = afterSequence;
  }

  push(event: TaskEvent): boolean {
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

  [Symbol.asyncIterator](): AsyncIterator<TaskEvent> {
    return { next: () => this.next() };
  }

  next(): Promise<IteratorResult<TaskEvent>> {
    const event = this.#queue.shift();
    if (event) return Promise.resolve({ value: event, done: false });
    if (this.#closed) return Promise.resolve({ value: undefined, done: true });
    return new Promise((resolve) => this.#waiters.push(resolve));
  }
}
