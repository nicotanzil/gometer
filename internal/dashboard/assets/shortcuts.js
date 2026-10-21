'use strict';

(function(root) {
  const navigation={b:'build',l:'live',h:'history',g:'guide',c:'coverage'};
  function editing(event) {
    const elements=event.composedPath?.()||[event.target];
    return elements.some(e=>e?.isContentEditable||e?.closest?.('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"],[role="combobox"],[role="searchbox"]'));
  }
  // This controller owns key interpretation only. Actions use the same application
  // handlers as buttons, including draft validation and run-admission checks.
  function controller({perform,allowed=()=>true,modal=()=>false,characters=()=>true,hint=()=>{},schedule=setTimeout,cancel=clearTimeout}) {
    let pending=false,timer;
    function reset(){pending=false;if(timer!==undefined)cancel(timer);timer=undefined;hint(false);}
    function activate(event,action){reset();if(!allowed(action))return;event.preventDefault();perform(action);}
    function keydown(event){
      if(event.defaultPrevented||event.repeat||event.isComposing||event.keyCode===229||modal()){reset();return;}
      const key=event.key.toLowerCase(),isEditing=editing(event),modifier=event.metaKey||event.ctrlKey;
      if(modifier){
        reset();if(event.altKey||event.metaKey&&event.ctrlKey)return;
        if(key==='enter')activate(event,event.shiftKey?'start':'validate');
        else if(key==='s'&&!event.shiftKey)activate(event,'save');
        else if(key==='z'&&!event.shiftKey&&!isEditing)activate(event,'undo');
        return;
      }
      if(event.altKey||isEditing||!characters()){reset();return;}
      if(key==='shift')return;
      if(key==='?'){activate(event,'help');return;}
      if(pending){reset();if(navigation[key]){activate(event,navigation[key]);return;}if(key==='escape'){event.preventDefault();return;}}
      if(key==='g'){event.preventDefault();pending=true;hint(true);timer=schedule(reset,2000);}
    }
    return {keydown,reset};
  }
  function create({allowed,perform,onError=()=>{}}){
    const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
    const mac=/Mac|iPhone|iPad/.test(navigator.platform),mod=mac?'⌘':'Ctrl';
    const preference='gometer.shortcuts.characters.v1';let enabled=true,previousFocus=null;
    try{enabled=localStorage.getItem(preference)!=='off';}catch{/* Preferences still work for this visit. */}
    const dialog=el('dialog',undefined,'shortcuts-dialog');dialog.id='keyboard-shortcuts-dialog';dialog.setAttribute('aria-labelledby','shortcuts-title');dialog.setAttribute('aria-describedby','shortcuts-intro');
    dialog.innerHTML='<form method="dialog" class="shortcuts-heading"><div><div class="eyebrow">WORK FASTER</div><h2 id="shortcuts-title">Keyboard shortcuts</h2></div><button class="text-button" aria-label="Close keyboard shortcuts" value="close">✕</button></form><p id="shortcuts-intro">Keep your hands on the keyboard. Press G, release it, then a destination key within two seconds.</p><div class="shortcuts-groups"><section><h3>Go to a view</h3><dl data-navigation></dl></section><section><h3>Build a test</h3><dl data-builder></dl><p class="helper">Builder actions use your current settings and the same checks as their buttons. Start run sends workload traffic.</p></section></div><div class="shortcuts-general"><span>Show this reference <kbd>?</kbd></span><span>Close dialog / cancel G sequence <kbd>Esc</kbd></span></div><label class="shortcuts-preference"><input type="checkbox" id="shortcut-characters"> Enable G sequences and ?</label><p class="helper">Character keys stay inactive in text fields and dialogs. Turn them off here if they interfere with your input tools. Modifier shortcuts stay available.</p><p class="helper">Canvas Undo works outside text fields. Your normal text undo, copy, paste, and browser shortcuts remain available.</p>';
    document.body.append(dialog);
    function row(label,keys){const r=el('div'),dt=el('dt',label),dd=el('dd');keys.forEach((k,i)=>{if(i)dd.append(el('span','then','shortcut-then'));dd.append(el('kbd',k));});r.append(dt,dd);return r;}
    for(const [key,view] of Object.entries(navigation))dialog.querySelector('[data-navigation]').append(row({build:'Build',live:'Live results',history:'Run history',guide:'User guide',coverage:'Feature coverage'}[view],['G',key.toUpperCase()]));
    for(const [name,keys] of [['Validate',[`${mod} Enter`]],['Save scenario',[`${mod} S`]],['Start run',[`${mod} Shift Enter`]],['Undo canvas edit',[`${mod} Z`]]])dialog.querySelector('[data-builder]').append(row(name,keys));
    const hint=el('div',undefined,'shortcut-sequence');hint.hidden=true;hint.setAttribute('role','status');hint.setAttribute('aria-live','polite');hint.textContent='Go to: B Build · L Live · H History · G Guide · C Coverage · Esc cancel';document.body.append(hint);
    function open(){if(document.querySelector('dialog[open]'))return;keys.reset();previousFocus=document.activeElement;dialog.showModal();}
    const keys=controller({allowed:action=>action==='help'||allowed(action),modal:()=>!!document.querySelector('dialog[open],#workspace-sidebar[aria-modal="true"]'),characters:()=>enabled,hint:visible=>{hint.hidden=!visible;},perform:action=>{try{Promise.resolve(action==='help'?open():perform(action)).catch(onError);}catch(e){onError(e);}}});
    const picker=dialog.querySelector('#shortcut-characters');picker.checked=enabled;
    function cues(){
      dialog.querySelector('[data-navigation]').parentElement.setAttribute('aria-disabled',String(!enabled));
      for(const button of document.querySelectorAll('[data-shortcuts]')){button.setAttribute('aria-haspopup','dialog');button.setAttribute('aria-controls',dialog.id);button.title=enabled?'Keyboard shortcuts (?)':'Keyboard shortcuts';button.querySelector('kbd')?.toggleAttribute('hidden',!enabled);}
      for(const button of document.querySelectorAll('.nav[data-view]')){const key=Object.keys(navigation).find(k=>navigation[k]===button.dataset.view);button.title=enabled&&key?`Go to ${button.dataset.view} (G then ${key.toUpperCase()})`:'';}
    }
    picker.addEventListener('change',()=>{enabled=picker.checked;keys.reset();cues();try{localStorage.setItem(preference,enabled?'on':'off');}catch{/* Current-visit behavior remains active. */}});
    dialog.addEventListener('close',()=>{keys.reset();if(previousFocus?.isConnected&&previousFocus.getClientRects().length)previousFocus.focus({preventScroll:true});});
    for(const button of document.querySelectorAll('[data-shortcuts]'))button.addEventListener('click',open);
    for(const [id,key] of [['validate','Enter'],['save-scenario','S'],['start','Shift+Enter']]){const button=document.getElementById(id);button.setAttribute('aria-keyshortcuts',`Meta+${key} Control+${key}`);button.title=`${id==='validate'?'Validate':id==='start'?'Start run':'Save scenario'} (${mod} ${key.replaceAll('+',' ')})`;}
    document.addEventListener('keydown',keys.keydown);
    document.addEventListener('pointerdown',keys.reset);
    document.addEventListener('focusin',e=>{if(editing(e)||dialog.open)keys.reset();});
    window.addEventListener('blur',keys.reset);cues();
    return {open};
  }
  const api={controller,create};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoMeterShortcuts=api;
})(globalThis);
