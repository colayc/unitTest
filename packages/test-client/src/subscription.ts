import type { TaskEvent } from "@unit-test-ide/protocol-models";

export class EventSubscription implements AsyncIterable<TaskEvent> {
  readonly #queue: TaskEvent[] = [];
  readonly #waiters: Array<(value: IteratorResult<TaskEvent>) => void> = [];
  #closed = false;
  lastSequence: number;

  constructor(afterSequence: number) {
    this.lastSequence = afterSequence;
  }

  push(event: TaskEvent): void {
    if (this.#closed || event.sequence <= this.lastSequence) return;
    this.lastSequence = event.sequence;
    const waiter = this.#waiters.shift();
    if (waiter) waiter({ value: event, done: false });
    else this.#queue.push(event);
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
