// Status-bar item showing the current permission mode and running
// USD cost. Click handler opens the BiuMind status command.

import * as vscode from 'vscode';

export class StatusBar implements vscode.Disposable {
  private item: vscode.StatusBarItem;

  mode: string;
  costUSD: number = 0;

  constructor(initialMode: string) {
    this.mode = initialMode;
    this.item = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Right,
      100,
    );
    this.item.command = 'biu.showStatus';
    this.item.tooltip = 'BiuMind — click for details';
    this.render();
    this.item.show();
  }

  setMode(mode: string): void {
    this.mode = mode;
    this.render();
  }

  setCost(usd: number): void {
    this.costUSD = usd;
    this.render();
  }

  private render(): void {
    // VS Code codicons: $(chip) for the agent, $(beaker) for plan
    // mode, $(circle-slash) for bypass. Tinting via colors.
    const symbol = symbolFor(this.mode);
    const cost = this.costUSD > 0 ? ` · $${this.costUSD.toFixed(4)}` : '';
    this.item.text = `$(${symbol}) BiuMind ${this.mode}${cost}`;
    this.item.backgroundColor = backgroundFor(this.mode);
  }

  dispose(): void {
    this.item.dispose();
  }
}

function symbolFor(mode: string): string {
  switch (mode) {
    case 'plan':
      return 'beaker';
    case 'acceptEdits':
      return 'check';
    case 'bypassPermissions':
      return 'warning';
    default:
      return 'chip';
  }
}

function backgroundFor(mode: string): vscode.ThemeColor | undefined {
  switch (mode) {
    case 'bypassPermissions':
      return new vscode.ThemeColor('statusBarItem.errorBackground');
    case 'plan':
      return new vscode.ThemeColor('statusBarItem.prominentBackground');
    default:
      return undefined;
  }
}
