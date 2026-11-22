import { useCallback, useEffect, useState } from 'react';
import { ExternalLink, GitCommitHorizontal, GitPullRequest, Play, Power, RefreshCw } from 'lucide-react';
import { api, APIError, type GameChange, type GameResult, type GamesResponse } from '../api';

const phases: Record<string,string>={Running:'En ligne',Suspended:'En veille',Restoring:'Restauration du monde',Updating:'Installation de la version',Starting:'Démarrage',Stopping:'Arrêt du jeu',BackingUp:'Sauvegarde du monde',WaitingForGateway:'Préparation de la passerelle',WaitingForBackup:'Sauvegarde en cours',Blocked:'Opération bloquée',Finalized:'Arrêt terminé'};

export function GameRoomsWidget(){
  const [data,setData]=useState<GamesResponse|null>(null);
  const [error,setError]=useState('');
  const [commandError,setCommandError]=useState('');
  const [busy,setBusy]=useState(false);
  const [selection,setSelection]=useState<{name:string;change:GameChange;preview:GameResult}|null>(null);
  const [result,setResult]=useState<GameResult|null>(null);
  const [versions,setVersions]=useState<Record<string,string>>({});
  const load=useCallback(async()=>{
    try{const value=await api.games();setData(value);setError('');return value;}
    catch(e){setError((e as Error).message);return null;}
  },[]);
  useEffect(()=>{let active=true;const refresh=()=>{if(active&&!document.hidden)void load();};refresh();const timer=setInterval(refresh,10000);return()=>{active=false;clearInterval(timer);};},[load]);
  const prepare=async(name:string,values:{state?:'Running'|'Suspended';version?:string})=>{
    if(!data||busy)return;setBusy(true);setCommandError('');setResult(null);
    const change:GameChange={...values,expectedRevision:data.desired.revision,requestId:crypto.randomUUID()};
    try{const preview=await api.previewGame(name,change);setSelection({name,change,preview});}
    catch(e){await load();setCommandError((e as Error).message);}finally{setBusy(false);}
  };
  const confirm=async()=>{
    if(!selection||busy)return;setBusy(true);setCommandError('');
    try{const value=await api.changeGame(selection.name,selection.change);setResult(value);setSelection(null);await load();}
    catch(e){if(e instanceof APIError&&e.status===409)setSelection(null);await load();setCommandError((e as Error).message);}finally{setBusy(false);}
  };
  if(!data)return <div className="games-empty"><p>{error||'Lecture des serveurs…'}</p>{error&&<><a href={`/auth/login?return=${encodeURIComponent(window.location.pathname)}`}>Se connecter</a><button className="retry" onClick={()=>void load()}><RefreshCw size={14} aria-hidden/> Réessayer</button></>}</div>;
  const synced=data.observed.argoSync==='Synced'&&data.observed.argoRevision===data.desired.revision;
  return <div className="games-management">
    <p className="games-intro">Démarre une partie, retrouve ton monde, puis mets le serveur en veille.</p>
    <div className="games-sync"><GitCommitHorizontal size={16} aria-hidden/><span>{synced?'Git synchronisé avec le cluster':'Synchronisation Git en attente'}</span><button className="retry" onClick={()=>void load()} disabled={busy} aria-label="Actualiser les serveurs"><RefreshCw size={14} aria-hidden/></button></div>
    {!data.canWrite&&<p className="note">{data.desired.workflowURL?<a href={data.desired.workflowURL} target="_blank" rel="noreferrer">Démarrer ou mettre en veille sur GitHub <ExternalLink size={13} aria-hidden/></a>:'Les commandes seront disponibles après configuration de l’accès GitHub du portail.'}</p>}
    {data.observationError&&<p className="problem" role="status">{data.observationError}. L’état affiché dans Git reste disponible.</p>}
    <div className="games-list">{data.desired.rooms.map(room=>{
      const live=data.observed.rooms?.[room.name];
      const current=!!live&&live.generation===live.observedGeneration&&live.state===room.state&&live.version===room.version&&synced;
      const ready=current&&live.phase==='Running';
      const phase=live?(phases[live.phase]||live.phase||'En attente du contrôleur'):'État du cluster indisponible';
      const selected=versions[room.name]||room.version;
      return <section className="game-room" key={room.name} aria-label={room.name}>
        <div className="game-room-title"><h3>{room.name}</h3><span className={`mark mark-${live?.phase==='Blocked'?'down':ready?'ok':'unknown'}`}>{current?phase:'Changement en cours'}</span></div>
        <p className="game-room-meta">Project Zomboid · {room.version}</p>
        <dl className="game-room-facts"><div><dt>Demandé dans Git</dt><dd>{room.state==='Running'?'En ligne':'En veille'}</dd></div><div><dt>État observé</dt><dd>{phase}{live&&live.generation!==live.observedGeneration?' · ancienne génération':''}</dd></div><div><dt>Sauvegarde</dt><dd>{live?.backupVerified?'Dernière sauvegarde vérifiée':'Pas encore de sauvegarde vérifiée'}</dd></div></dl>
        {ready&&room.endpoint&&<p className="game-connect">Rejoindre <code>{room.endpoint}</code></p>}
        {live?.message&&<p className="problem" role="status">{live.message}</p>}
        <div className="game-room-controls"><button className={room.state==='Suspended'?'primary':''} disabled={!data.canWrite||busy} onClick={()=>void prepare(room.name,{state:room.state==='Running'?'Suspended':'Running'})}>{room.state==='Running'?<Power size={15} aria-hidden/>:<Play size={15} aria-hidden/>}{room.state==='Running'?'Mettre en veille':'Démarrer'}</button>
          <label>Version<select aria-label={`Version de ${room.name}`} disabled={!data.canWrite||busy||!data.desired.versions.length} value={selected} onChange={e=>setVersions(v=>({...v,[room.name]:e.target.value}))}>{!data.desired.versions.includes(room.version)&&<option value={room.version}>{room.version}</option>}{data.desired.versions.map(version=><option key={version} value={version}>{version}</option>)}</select></label>
          <button disabled={!data.canWrite||busy||selected===room.version} onClick={()=>void prepare(room.name,{version:selected})}><GitPullRequest size={15} aria-hidden/> Proposer la version</button>
        </div>
      </section>;
    })}</div>
    {selection&&<section className="game-confirm" aria-label="Vérifier le changement Git"><h3>{selection.change.version?'Proposer une mise à jour':'Vérifier la commande'}</h3><p>{selection.change.version?'Une pull request sera créée. La version changera après sa fusion.':selection.change.state==='Suspended'?'Le contrôleur arrêtera le jeu, vérifiera une sauvegarde et libérera la passerelle si aucun autre serveur ne l’utilise.':'Le contrôleur préparera le monde et la passerelle avant de démarrer le jeu.'}</p><pre>{selection.preview.diff}</pre><div className="game-confirm-actions"><button className="primary" disabled={busy} onClick={()=>void confirm()}>{busy?'Enregistrement…':selection.change.version?'Créer la pull request':'Enregistrer dans Git'}</button><button disabled={busy} onClick={()=>setSelection(null)}>Annuler</button></div></section>}
    {result&&<p className="game-result" role="status">{result.kind==='pull_request'?'Pull request créée. La version reste inchangée jusqu’à sa fusion.':result.kind==='unchanged'?'Cet état était déjà demandé dans Git.':'Commande enregistrée dans Git. Le contrôleur applique le changement.'} {result.url&&<a href={result.url} target="_blank" rel="noreferrer">{result.kind==='pull_request'?'Voir la pull request':'Voir le commit'} <ExternalLink size={13} aria-hidden/></a>}</p>}
    {error&&<p className="problem" role="alert">{error}</p>}
    {commandError&&<p className="problem" role="alert">{commandError}</p>}
  </div>;
}
