import { expect, test } from '@playwright/test';

const before='a'.repeat(40),after='b'.repeat(40);
function snapshot(){return {desired:{revision:before,writable:true,versions:['v1','v2'],rooms:[{name:'one',state:'Suspended',version:'v1',endpoint:'example.invalid:17261'}]},observed:{argoRevision:before,argoSync:'Synced',rooms:{one:{generation:1,observedGeneration:1,state:'Suspended',version:'v1',phase:'Suspended',backupVerified:true}}},canWrite:true};}

test('game command previews its diff and waits for observed readiness',async({page})=>{
  const state=snapshot();let preview:any;
  await page.route('**/api/games**',async route=>{
    const req=route.request();
    if(req.method()==='GET')return route.fulfill({json:state});
    if(req.url().endsWith('/preview')){preview=req.postDataJSON();return route.fulfill({json:{kind:'preview',revision:state.desired.revision,diff:preview.version?'- version: v1\n+ version: v2':'- state: Suspended\n+ state: Running'}});}
    expect(req.postDataJSON()).toEqual(preview);
    if(preview.version)return route.fulfill({json:{kind:'pull_request',revision:after,url:'https://github.com/example/lab/pull/1',diff:'version'}});
    state.desired.revision=after;state.desired.rooms[0].state='Running';
    return route.fulfill({json:{kind:'commit',revision:after,url:`https://github.com/example/lab/commit/${after}`,diff:'state'}});
  });
  await page.request.put('/api/layout',{data:{pages:[{name:'Jeux',slug:'jeux',columns:[{size:'full',widgets:[{id:'games',type:'gamerooms'}]}]}]}});
  await page.goto('/jeux');
  await page.getByRole('button',{name:'Démarrer',exact:true}).click();
  await expect(page.getByLabel('Vérifier le changement Git')).toContainText('+ state: Running');
  expect(preview.expectedRevision).toBe(before);expect(preview.requestId).toBeTruthy();
  await page.getByRole('button',{name:'Enregistrer dans Git'}).click();
  await expect(page.getByRole('status')).toContainText('Commande enregistrée');
  await expect(page.getByText('Rejoindre',{exact:false})).toHaveCount(0);
  state.observed.argoRevision=after;state.observed.rooms.one={generation:2,observedGeneration:2,state:'Running',version:'v1',phase:'Running',backupVerified:true};
  await page.getByRole('button',{name:'Actualiser les serveurs'}).click();
  await expect(page.getByText('Rejoindre',{exact:false})).toBeVisible();
  await page.getByLabel('Version de one').selectOption('v2');
  await page.getByRole('button',{name:'Proposer la version'}).click();
  await page.getByRole('button',{name:'Créer la pull request'}).click();
  await expect(page.getByRole('status')).toContainText('reste inchangée');
  expect(state.desired.rooms[0].version).toBe('v1');
});

test('a stale Git view requires a new preview',async({page})=>{
  const state=snapshot();
  await page.route('**/api/games**',async route=>{
    if(route.request().method()==='GET')return route.fulfill({json:state});
    if(route.request().url().endsWith('/preview'))return route.fulfill({json:{kind:'preview',revision:before,diff:'state'}});
    state.desired.revision=after;
    return route.fulfill({status:409,json:{error:'Git a changé ; rechargez les serveurs avant de réessayer'}});
  });
  await page.request.put('/api/layout',{data:{pages:[{name:'Jeux',slug:'jeux',columns:[{size:'full',widgets:[{id:'games',type:'gamerooms'}]}]}]}});
  await page.goto('/jeux');await page.getByRole('button',{name:'Démarrer',exact:true}).click();await page.getByRole('button',{name:'Enregistrer dans Git'}).click();
  await expect(page.getByRole('alert')).toContainText('Git a changé');
  await expect(page.getByLabel('Vérifier le changement Git')).toHaveCount(0);
});
