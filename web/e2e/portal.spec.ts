import { expect, test, type Page } from '@playwright/test';

const reset = (page: Page) => page.request.delete('/api/layout');

test.beforeEach(async ({ page }) => {
  await reset(page);
  await page.goto('/');
  await page.evaluate(() => localStorage.clear());
  await page.goto('/');
  await expect(page.locator('.widget').first()).toBeVisible();
});

test('shows the configured pages and widgets', async ({ page }) => {
  await expect(page).toHaveTitle(/Accueil \| Test Lab/);
  await expect(page.locator('.band nav a')).toHaveText(['Accueil', 'Outils']);
  await expect(page.locator('.widget h2')).toContainText(['Horloges', 'Applications']);
  await expect(page.locator('.widget-bookmarks .link')).toContainText('Grafana');
});

test('the command palette navigates with the keyboard', async ({ page }) => {
  await page.keyboard.press('Control+k');
  await expect(page.locator('.palette[open]')).toBeVisible();
  await page.keyboard.type('outils');
  await expect(page.locator('.palette li').first()).toContainText('Outils');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/outils$/);
  await expect(page.locator('.widget h2')).toHaveText(['Heure locale']);
  await page.goBack();
  await expect(page.locator('.band nav a[aria-current="page"]')).toHaveText('Accueil');
});

test('a widget added in edit mode is saved and survives a reload', async ({ page }) => {
  await page.getByRole('button', { name: 'Modifier' }).click();
  await page.locator('.column-edit').first().getByRole('button', { name: 'Ajouter un widget' }).click();
  await page.locator('.catalog button', { hasText: 'Météo' }).click();
  await page.locator('#field-location').fill('Lyon');
  await page.getByRole('button', { name: 'Appliquer' }).click();
  await page.getByRole('button', { name: 'Enregistrer' }).click();
  await expect(page.locator('.toast')).toContainText('Disposition enregistrée');

  await page.reload();
  await expect(page.locator('.widget-weather')).toBeVisible();
  const config = await (await page.request.get('/api/config')).json();
  expect(config.source).toBe('custom');

  await page.getByRole('button', { name: 'Modifier' }).click();
  await page.getByRole('button', { name: 'Revenir au YAML' }).click();
  await expect(page.locator('.toast')).toContainText('rétablie');
  await expect(page.locator('.widget-weather')).toHaveCount(0);
});

test('links are edited with a form, not YAML', async ({ page }) => {
  await page.getByRole('button', { name: 'Modifier' }).click();
  await page.locator('.widget-bookmarks').getByRole('button', { name: /^Régler/ }).click();
  await page.getByRole('button', { name: 'Ajouter un lien' }).click();
  const entry = page.locator('.list-nested .list-entry').last();
  await entry.getByLabel('Nom', { exact: true }).fill('Proxmox');
  await entry.getByLabel('Adresse', { exact: true }).fill('https://pve.example');
  await page.getByRole('button', { name: 'Appliquer' }).click();
  await expect(page.locator('.widget-bookmarks .link')).toContainText(['Grafana', 'Proxmox']);
});

test('an unsaved layout is restored after a reload', async ({ page }) => {
  await page.getByRole('button', { name: 'Modifier' }).click();
  await page.getByLabel('Nom de la page').fill('Tableau de bord');
  await page.reload();
  await expect(page.locator('.band nav a').first()).toHaveText('Accueil');
  await page.getByRole('button', { name: 'Modifier' }).click();
  await expect(page.locator('.toast')).toContainText('Brouillon');
  await expect(page.getByLabel('Nom de la page')).toHaveValue('Tableau de bord');
  await page.getByRole('button', { name: 'Annuler' }).click();
  await page.getByRole('button', { name: 'Modifier' }).click();
  await expect(page.getByLabel('Nom de la page')).toHaveValue('Accueil');
});

test('widgets can be reordered with the keyboard', async ({ page }) => {
  await page.getByRole('button', { name: 'Modifier' }).click();
  const column = page.locator('.column-edit').nth(1);
  await expect(column.locator('.widget h2')).toHaveText(['Recherche', 'Applications']);
  await column.locator('.grip').first().focus();
  // The drag sensor measures the layout between key presses.
  await page.keyboard.press('Space');
  await page.waitForTimeout(250);
  await page.keyboard.press('ArrowDown');
  await page.waitForTimeout(250);
  await page.keyboard.press('Space');
  await expect(column.locator('.widget h2')).toHaveText(['Applications', 'Recherche']);
});

test('kiosk mode hides the controls', async ({ page }) => {
  await page.goto('/?kiosk=10');
  await expect(page.locator('.widget').first()).toBeVisible();
  await expect(page.locator('.band-tools')).toBeHidden();
  await page.getByRole('button', { name: 'Quitter le mode kiosque' }).click({ force: true });
  await expect(page.locator('.band-tools')).toBeVisible();
});

test('the phone layout does not scroll sideways', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await page.goto('/');
  await expect(page.locator('.widget').first()).toBeVisible();
  const width = () => page.evaluate(() => document.documentElement.scrollWidth);
  expect(await width()).toBeLessThanOrEqual(390);

  // Header: tabs stay on one row beside nothing else, tools keep to icons.
  const nav = await page.locator('.band nav').boundingBox();
  expect(nav!.height).toBeLessThan(60);
  await expect(page.locator('.btn-label')).toBeHidden();

  await page.locator('.band nav a', { hasText: 'Outils' }).click();
  await expect(page).toHaveURL(/\/outils$/);
  await page.getByRole('button', { name: 'Modifier' }).click();
  await expect(page.locator('.editbar')).toBeVisible();
  expect(await width()).toBeLessThanOrEqual(390);
});

test('a section folds the widgets below it and remembers the choice', async ({ page }) => {
  const layout = {
    pages: [
      {
        name: 'Accueil',
        group: 'Lab',
        columns: [
          {
            size: 'full',
            widgets: [
              { id: 'top', type: 'clock', title: 'Avant' },
              { id: 'sec', type: 'section', title: 'Outils' },
              { id: 'in1', type: 'clock', title: 'Dedans' },
              { id: 'in2', type: 'search' },
            ],
          },
        ],
      },
      { name: 'Autre', group: 'Perso', columns: [{ size: 'full', widgets: [{ id: 'c', type: 'clock' }] }] },
    ],
  };
  expect((await page.request.put('/api/layout', { data: layout })).ok()).toBeTruthy();
  await page.goto('/');
  // Groups only separate the tabs: no caption, since a caption is not a link.
  await expect(page.locator('.nav-sep')).toHaveCount(1);
  await expect(page.locator('.band nav')).not.toContainText('Lab');
  const toggle = page.getByRole('button', { name: 'Outils' });
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(page.locator('.widget h2')).toContainText(['Avant', 'Dedans']);
  await toggle.click();
  await expect(page.locator('.widget h2')).toHaveText(['Avant']);
  await expect(page.locator('.section-count')).toHaveText('2 widgets');
  await page.reload();
  await expect(page.getByRole('button', { name: /Outils/ })).toHaveAttribute('aria-expanded', 'false');
});
