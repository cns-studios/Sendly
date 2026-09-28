// Transfers announcement card on the homepage. Markup: web/templates/transfers-promo.html,
// styles: web/static/css/transfers-promo.css. Delete all three (and the template call in
// index.html plus the transfers_promo_* i18n keys) to remove it.
(function(){
    var card=document.getElementById('transfers-promo');
    if(!card)return;
    // once dismissed, never shown again in this browser
    var DISMISSED_KEY='sendly_transfers_promo_dismissed';
    try{if(localStorage.getItem(DISMISSED_KEY)){card.remove();return;}}catch(e){}
    // wait while a modal (terms, device approval, ...) is open
    function modalOpen(){return !!document.querySelector('.tos-overlay:not(.hidden)');}
    function dismiss(){
        try{localStorage.setItem(DISMISSED_KEY,'1');}catch(e){}
        card.classList.add('is-leaving');
        card.classList.remove('is-visible');
        setTimeout(function(){card.remove();},400);
    }
    function show(){
        if(!card.isConnected||card.classList.contains('is-visible'))return;
        if(modalOpen())return;
        observer.disconnect();
        card.classList.add('is-visible');
    }
    var observer=new MutationObserver(show);
    document.getElementById('tp-close').addEventListener('click',dismiss);
    document.addEventListener('keydown',function(e){if(e.key==='Escape'&&card.classList.contains('is-visible')&&!modalOpen())dismiss();});
    setTimeout(function(){
        document.querySelectorAll('.tos-overlay').forEach(function(el){observer.observe(el,{attributes:true,attributeFilter:['class']});});
        show();
    },700);
})();
